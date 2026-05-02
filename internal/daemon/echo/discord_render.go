package echo

import (
	"encoding/json"
	"strings"
)

// discordEmbedDescriptionLimit caps the description rendered to
// Discord's wire format. Discord's documented hard limit is 4096
// runes per embed description; trimming at 4000 leaves headroom for
// the appended ellipsis without ever crossing the limit. Mirrors the
// v7.0 outbound adapter's defensive cap (`relay.discordEmbedDescriptionLimit`).
const discordEmbedDescriptionLimit = 4000

// discordRenderEllipsis is appended to truncated descriptions so the
// trim is visible in the channel.
const discordRenderEllipsis = "…"

// RenderInteraction converts a transport-agnostic CommandResponse
// into the JSON wire format Discord expects for a CHANNEL_MESSAGE_WITH_SOURCE
// (`type: 4`) interaction response.
//
// Translation rules (mirrors the doc in `commands.go` / `Block`):
//
//   - "header"  → `embeds[0].title` (the first header block becomes the
//                 title of the first embed).
//   - "section" → `embeds[*].description` — successive sections after
//                 a header are concatenated with `\n\n`. A section
//                 BEFORE any header lands as a top-level `data.content`
//                 line plus a fallback embed description (so clients
//                 that strip embeds still see the gist).
//   - "context" → `embeds[*].footer.text` (last context wins per embed).
//   - "divider" → starts a new embed (Discord embeds visually separate
//                 themselves; using one embed per logical section
//                 keeps long status responses readable).
//   - response.Ephemeral → `data.flags = 64`.
//
// The output is the raw JSON bytes that the handler writes verbatim
// to the response — no embedding inside an envelope, no escaping
// games. A nil resp produces a minimal "no content" ack so the
// transport layer never has to special-case nil.
func RenderInteraction(resp *CommandResponse) []byte {
	if resp == nil {
		return []byte(`{"type":4,"data":{"content":""}}`)
	}

	embeds := blocksToEmbeds(resp.Blocks)
	data := map[string]any{}
	if resp.Text != "" {
		data["content"] = resp.Text
	}
	if len(embeds) > 0 {
		data["embeds"] = embeds
	}
	if resp.Ephemeral {
		data["flags"] = discordEphemeralFlag
	}

	out := map[string]any{
		"type": discordResponseChannelMessageWithSource,
		"data": data,
	}
	body, err := json.Marshal(out)
	if err != nil {
		// json.Marshal on a map of plain values has no realistic
		// failure mode; if it does happen the safest bet is to fall
		// back to a minimal ack so Discord doesn't see a 500.
		return []byte(`{"type":4,"data":{"content":""}}`)
	}
	return body
}

// discordEmbed is the subset of Discord's embed schema Watchfire
// emits. Only what's used is present — Discord supports many more
// fields but they're not needed for v8.0 slash-command responses.
//
// https://discord.com/developers/docs/resources/channel#embed-object
type discordEmbed struct {
	Title       string              `json:"title,omitempty"`
	Description string              `json:"description,omitempty"`
	Color       int                 `json:"color,omitempty"`
	Footer      *discordEmbedFooter `json:"footer,omitempty"`
}

type discordEmbedFooter struct {
	Text string `json:"text,omitempty"`
}

// blocksToEmbeds converts a Block slice into a Discord embeds slice.
// The translation is intentionally lossy — Block Kit's full
// expressiveness doesn't fit into Discord embeds, so the rule is
// "preserve the structure that matters for the three v8.0 commands"
// (status / retry / cancel) rather than chase 1:1 fidelity.
func blocksToEmbeds(blocks []Block) []*discordEmbed {
	if len(blocks) == 0 {
		return nil
	}
	embeds := make([]*discordEmbed, 0)
	current := &discordEmbed{}
	hasContent := false
	descParts := []string{}

	flush := func() {
		if !hasContent && len(descParts) == 0 {
			return
		}
		if len(descParts) > 0 {
			joined := strings.Join(descParts, "\n\n")
			current.Description = trimDescription(joined)
		}
		embeds = append(embeds, current)
		current = &discordEmbed{}
		hasContent = false
		descParts = nil
	}

	for _, b := range blocks {
		switch b.Type {
		case "header":
			// A new header starts a fresh embed when the current
			// one already has substance — otherwise reuse.
			if hasContent || len(descParts) > 0 {
				flush()
			}
			current.Title = b.Text
			hasContent = true
		case "section":
			if b.Text != "" {
				descParts = append(descParts, b.Text)
				hasContent = true
			}
		case "context":
			if b.Text != "" {
				current.Footer = &discordEmbedFooter{Text: b.Text}
				hasContent = true
			}
		case "divider":
			flush()
		}
	}
	flush()
	if len(embeds) == 0 {
		return nil
	}
	return embeds
}

// trimDescription enforces Discord's per-embed description rune cap
// with a visible ellipsis. The rune-count walk avoids splitting a
// multi-byte sequence in half — substring slicing on raw bytes can
// produce invalid UTF-8 if a description ends in the middle of a
// codepoint.
func trimDescription(s string) string {
	count := 0
	cut := 0
	for i := range s {
		if count == discordEmbedDescriptionLimit {
			cut = i
			break
		}
		count++
	}
	if cut == 0 {
		return s
	}
	return s[:cut] + discordRenderEllipsis
}
