package telegram

import (
	"context"
	"fmt"
	"html"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/watchfire-io/watchfire/internal/config"
	"github.com/watchfire-io/watchfire/internal/daemon/echo"
	"github.com/watchfire-io/watchfire/internal/daemon/telegrambot"
	"github.com/watchfire-io/watchfire/internal/models"
)

// CommandContextFactory builds the echo.CommandContext a paired chat's
// commands run against. The production factory is the daemon server's
// telegramCommandContextFor (built on the task-0133 callbacks); tests
// inject fakes.
type CommandContextFactory func(chatID, userID int64) echo.CommandContext

// maxBackoff caps the exponential backoff between failed getUpdates
// polls.
const maxBackoff = 60 * time.Second

// Config carries everything a Bridge needs. Constructed by
// NewFromConfig in production; tests build it directly.
type Config struct {
	// Token is the resolved bot token (from the keyring).
	Token string
	// Pairing is the server-owned pairing manager. The bridge redeems
	// codes against it; it survives bridge restarts.
	Pairing *Pairing
	// Hostname names this daemon in the pairing welcome message.
	Hostname string
	// PairedChats seeds the in-memory allowlist snapshot.
	PairedChats []models.TelegramPairedChat
	// PollTimeout is the getUpdates long-poll window (0 → the client
	// default). Tests use a short window against the httptest fake.
	PollTimeout time.Duration
	// Client is the Bot API client (nil → telegrambot.New()).
	Client *telegrambot.Client
	// Logger receives bridge lifecycle + error lines (nil → log.Default()).
	Logger *log.Logger
	// CommandContextFor scopes paired-chat commands. nil disables the
	// command surface (pairing still works) — production always wires
	// the server factory in.
	CommandContextFor CommandContextFactory
}

// Bridge owns the getUpdates long-poll loop. Pairing (/start, /pair —
// task 0136) admits chats onto the allowlist; paired chats get the
// read-only command surface (/projects /use /status /tasks /help —
// task 0137). Run-control verbs (/run, /say, …) arrive in 0142.
//
// The bridge only ever reads from the daemon: it never calls
// AgentService.Resize and never writes to any agent PTY.
type Bridge struct {
	token       string
	pairing     *Pairing
	hostname    string
	pollTimeout time.Duration
	client      *telegrambot.Client
	logger      *log.Logger
	cmdCtxFor   CommandContextFactory

	mu     sync.Mutex
	paired map[int64]models.TelegramPairedChat
	// lastProjects remembers, per chat, the ordering of the most recent
	// /projects listing so "/use 2" means "the 2nd row of the list I'm
	// looking at". In-memory only — after a restart /use falls back to
	// the live FindProjects order, which /projects prints from anyway.
	lastProjects map[int64][]echo.ProjectInfo
	// offset is the next getUpdates offset (highest seen update_id + 1).
	// Kept in memory only — a daemon restart re-reads the backlog, and
	// Telegram drops acknowledged updates server-side.
	offset  int64
	botUser string // cached getMe username

	// sleepFn + persistFn + setDefaultFn are test seams; production
	// uses the defaults set in New.
	sleepFn      func(ctx context.Context, d time.Duration)
	persistFn    func(chat models.TelegramPairedChat) error
	setDefaultFn func(chatID int64, projectID string) error
}

// New builds a Bridge from an explicit Config.
func New(cfg Config) *Bridge {
	client := cfg.Client
	if client == nil {
		client = telegrambot.New()
	}
	logger := cfg.Logger
	if logger == nil {
		logger = log.Default()
	}
	paired := make(map[int64]models.TelegramPairedChat, len(cfg.PairedChats))
	for _, c := range cfg.PairedChats {
		paired[c.ChatID] = c
	}
	return &Bridge{
		token:        cfg.Token,
		pairing:      cfg.Pairing,
		hostname:     cfg.Hostname,
		pollTimeout:  cfg.PollTimeout,
		client:       client,
		logger:       logger,
		cmdCtxFor:    cfg.CommandContextFor,
		paired:       paired,
		lastProjects: make(map[int64][]echo.ProjectInfo),
		sleepFn:      sleepCtx,
		persistFn:    persistPairedChat,
		setDefaultFn: persistDefaultProject,
	}
}

// NewFromConfig builds the production Bridge from the loaded
// integrations config. Returns nil — meaning "do not start anything" —
// unless Telegram is enabled AND a bot token resolved from the keyring.
func NewFromConfig(cfg *models.IntegrationsConfig, pairing *Pairing, hostname string, cmdCtxFor CommandContextFactory, logger *log.Logger) *Bridge {
	if cfg == nil || cfg.Telegram == nil || !cfg.Telegram.Enabled {
		return nil
	}
	if cfg.Telegram.BotToken == "" {
		if cfg.Telegram.BotTokenRef != "" && logger != nil {
			logger.Printf("WARN: telegram bridge: bot token reference %q not in keyring — bridge disabled", cfg.Telegram.BotTokenRef)
		}
		return nil
	}
	return New(Config{
		Token:             cfg.Telegram.BotToken,
		Pairing:           pairing,
		Hostname:          hostname,
		PairedChats:       cfg.Telegram.PairedChats,
		CommandContextFor: cmdCtxFor,
		Logger:            logger,
	})
}

// Run drives the long-poll loop until ctx is cancelled. Network / API
// errors back off exponentially (1s doubling to 60s); 429s honour the
// server's retry_after instead.
func (b *Bridge) Run(ctx context.Context) {
	// Register the command set for Telegram's client-side autocomplete.
	// Best-effort: a failure costs autocompletion, not functionality.
	if err := b.client.SetMyCommands(ctx, b.token, botCommands()); err != nil && ctx.Err() == nil {
		b.logger.Printf("WARN: telegram bridge: setMyCommands failed: %v", err)
	}
	backoff := time.Second
	for {
		if ctx.Err() != nil {
			return
		}
		b.mu.Lock()
		offset := b.offset
		b.mu.Unlock()
		updates, err := b.client.GetUpdates(ctx, b.token, offset, b.pollTimeout)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			wait := backoff
			if ra := telegrambot.RetryAfter(err); ra > 0 {
				wait = ra
			} else {
				backoff *= 2
				if backoff > maxBackoff {
					backoff = maxBackoff
				}
			}
			b.logger.Printf("WARN: telegram bridge: getUpdates failed (retrying in %s): %v", wait, err)
			b.sleepFn(ctx, wait)
			continue
		}
		backoff = time.Second
		for _, u := range updates {
			// Advance the offset before handling so a slow or failing
			// handler can never cause an update to be reprocessed.
			if u.UpdateID >= offset {
				offset = u.UpdateID + 1
				b.mu.Lock()
				b.offset = offset
				b.mu.Unlock()
			}
			b.handleUpdate(ctx, u)
		}
	}
}

// BotUsername returns the bot's username, resolving it via getMe on
// first call and caching the result (pairing deep links are built from
// it).
func (b *Bridge) BotUsername(ctx context.Context) (string, error) {
	b.mu.Lock()
	cached := b.botUser
	b.mu.Unlock()
	if cached != "" {
		return cached, nil
	}
	user, err := b.client.GetMe(ctx, b.token)
	if err != nil {
		return "", err
	}
	b.mu.Lock()
	b.botUser = user.Username
	b.mu.Unlock()
	return user.Username, nil
}

// CachedBotUsername returns the getMe username if already resolved,
// without any network call.
func (b *Bridge) CachedBotUsername() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.botUser
}

// Revoke drops a chat from the in-memory allowlist immediately. The
// caller (RevokeTelegramChat RPC) persists the removal separately.
func (b *Bridge) Revoke(chatID int64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.paired, chatID)
}

// IsPaired reports whether chatID is on the live allowlist.
func (b *Bridge) IsPaired(chatID int64) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	_, ok := b.paired[chatID]
	return ok
}

// handleUpdate dispatches one getUpdates entry. /start and /pair are
// handled for everyone (they're the only way onto the allowlist);
// paired chats additionally get the command surface and inline-button
// callbacks. Everything else — edited messages, media, and any text
// from unpaired chats — is dropped without a reply, so an unpaired
// stranger probing the bot learns nothing.
func (b *Bridge) handleUpdate(ctx context.Context, u telegrambot.Update) {
	if u.CallbackQuery != nil {
		b.handleCallback(ctx, u.CallbackQuery)
		return
	}
	msg := u.Message
	if msg == nil || msg.From == nil || msg.Text == "" {
		return
	}
	cmd, rest := splitCommand(msg.Text)
	if cmd == "/start" || cmd == "/pair" {
		arg := ""
		if fields := strings.Fields(rest); len(fields) > 0 {
			arg = fields[0]
		}
		b.handlePairing(ctx, msg, arg)
		return
	}
	if !b.IsPaired(msg.Chat.ID) {
		return // unpaired silence — unchanged from 0136
	}
	if cmd == "" {
		return // plain text from a paired chat; /say arrives in 0142
	}
	b.dispatchCommand(ctx, msg, cmd, rest)
}

// handlePairing runs the /start / /pair flow for one message (any
// chat, paired or not — redeeming a fresh code from an already-paired
// chat simply refreshes its record).
func (b *Bridge) handlePairing(ctx context.Context, msg *telegrambot.Message, arg string) {
	chatID := msg.Chat.ID

	if b.pairing != nil && b.pairing.Consume(arg) {
		chat := models.TelegramPairedChat{
			ChatID:   chatID,
			UserID:   msg.From.ID,
			Username: msg.From.Username,
			PairedAt: time.Now().UTC(),
		}
		if err := b.persistFn(chat); err != nil {
			b.logger.Printf("ERROR: telegram bridge: persist paired chat %d: %v", chatID, err)
			b.reply(ctx, chatID, "Pairing failed on the daemon side — please run <code>watchfire telegram pair</code> again.")
			return
		}
		b.mu.Lock()
		b.paired[chatID] = chat
		b.mu.Unlock()
		b.pairing.Complete(chat)
		b.logger.Printf("INFO: telegram bridge: paired chat %d (@%s)", chatID, msg.From.Username)
		b.reply(ctx, chatID, "🔥 Paired with Watchfire on <b>"+html.EscapeString(b.hostname)+"</b>.\nSend /help to see what you can do.")
		return
	}

	// Bad or absent code. Paired chats get a gentle nudge; unpaired
	// chats get the pairing instructions — and nothing else, ever.
	if b.IsPaired(chatID) {
		b.reply(ctx, chatID, "This chat is already paired. Send /help to see available commands.")
		return
	}
	b.reply(ctx, chatID, "This Watchfire bot only talks to paired chats.\nOn the machine running Watchfire, run <code>watchfire telegram pair</code> and open the link it prints (or send <code>/pair &lt;code&gt;</code> here).")
}

// reply sends best-effort — a failed send is logged, never retried
// (Telegram redelivers nothing here; the user can just resend).
func (b *Bridge) reply(ctx context.Context, chatID int64, text string) {
	if _, err := b.client.SendMessage(ctx, b.token, chatID, text); err != nil {
		b.logger.Printf("WARN: telegram bridge: sendMessage to %d failed: %v", chatID, err)
	}
}

// splitCommand splits a message into its leading bot command and the
// remainder of the line (whitespace-normalized — project names keep
// their internal single spaces). Telegram appends "@botname" to
// commands in group chats — that suffix is stripped so
// "/pair@WatchfireBot ABC" parses the same as "/pair ABC". Non-command
// text returns cmd == "".
func splitCommand(text string) (cmd, rest string) {
	fields := strings.Fields(strings.TrimSpace(text))
	if len(fields) == 0 || !strings.HasPrefix(fields[0], "/") {
		return "", ""
	}
	cmd = strings.ToLower(fields[0])
	if at := strings.IndexByte(cmd, '@'); at >= 0 {
		cmd = cmd[:at]
	}
	return cmd, strings.Join(fields[1:], " ")
}

// parseCommand is splitCommand narrowed to the first argument — the
// shape the single-token pairing flow wants.
func parseCommand(text string) (cmd, arg string) {
	cmd, rest := splitCommand(text)
	if fields := strings.Fields(rest); len(fields) > 0 {
		arg = fields[0]
	}
	return cmd, arg
}

// persistPairedChat is the production persist hook: load → upsert by
// chat_id → save through config.SaveIntegrations, which keeps the bot
// token in the keyring and out of the YAML. Re-pairing an existing chat
// refreshes user/username/paired_at but preserves the per-chat
// default_project_id / muted / watch settings.
func persistPairedChat(chat models.TelegramPairedChat) error {
	cfg, err := config.LoadIntegrations()
	if err != nil {
		return err
	}
	if cfg.Telegram == nil {
		// The bridge only runs when Telegram is configured, but the user
		// may have deleted the integration mid-pairing; recreate the
		// minimal enabled shell rather than dropping the pairing.
		cfg.Telegram = &models.TelegramConfig{Enabled: true}
	}
	replaced := false
	for i := range cfg.Telegram.PairedChats {
		if cfg.Telegram.PairedChats[i].ChatID != chat.ChatID {
			continue
		}
		chat.DefaultProjectID = cfg.Telegram.PairedChats[i].DefaultProjectID
		chat.Muted = cfg.Telegram.PairedChats[i].Muted
		chat.Watch = cfg.Telegram.PairedChats[i].Watch
		cfg.Telegram.PairedChats[i] = chat
		replaced = true
		break
	}
	if !replaced {
		cfg.Telegram.PairedChats = append(cfg.Telegram.PairedChats, chat)
	}
	return config.SaveIntegrations(cfg)
}

// persistDefaultProject is the production /use persist hook: load →
// set the chat's default_project_id → save. Same
// config.SaveIntegrations path as pairing, so the selection lands in
// integrations.yaml and survives daemon restarts.
func persistDefaultProject(chatID int64, projectID string) error {
	cfg, err := config.LoadIntegrations()
	if err != nil {
		return err
	}
	if cfg.Telegram == nil {
		return fmt.Errorf("telegram is not configured")
	}
	for i := range cfg.Telegram.PairedChats {
		if cfg.Telegram.PairedChats[i].ChatID != chatID {
			continue
		}
		cfg.Telegram.PairedChats[i].DefaultProjectID = projectID
		return config.SaveIntegrations(cfg)
	}
	return fmt.Errorf("chat %d is not paired", chatID)
}

// sleepCtx sleeps for d or until ctx is cancelled, whichever comes
// first.
func sleepCtx(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}
