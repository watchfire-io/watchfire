package relay

import "strings"

// slackColorEmoji maps a project hex color to the closest Slack
// `:large_<color>_square:` emoji shortcode. The eight entries cover the
// v4.0 Beacon swatches enumerated in `internal/models/project.go::ProjectColors`;
// any unmapped value falls through to slackColorFallbackEmoji so a future
// palette change never silently strips the swatch from the message.
var slackColorEmoji = map[string]string{
	"#ef4444": ":large_red_square:",    // red
	"#f97316": ":large_orange_square:", // orange
	"#eab308": ":large_yellow_square:", // yellow
	"#22c55e": ":large_green_square:",  // green
	"#14b8a6": ":large_green_square:",  // teal → green (no `teal_square`)
	"#06b6d4": ":large_blue_square:",   // cyan → blue (no `cyan_square`)
	"#3b82f6": ":large_blue_square:",   // blue
	"#8b5cf6": ":large_purple_square:", // violet
	"#a855f7": ":large_purple_square:", // purple
	"#ec4899": ":large_purple_square:", // pink → purple (no `pink_square`)
}

// slackColorFallbackEmoji is the swatch used when the project hex is
// missing or doesn't match any known palette entry. `:white_square:` is
// the closest visually-neutral square emoji Slack ships by default.
const slackColorFallbackEmoji = ":white_square:"

// slackEmojiForColor resolves the project hex color to a Slack emoji
// shortcode. Lookup is case-insensitive; a missing or malformed value
// returns the fallback so the rendered context block never carries a
// raw hex string.
func slackEmojiForColor(hex string) string {
	if hex == "" {
		return slackColorFallbackEmoji
	}
	if e, ok := slackColorEmoji[strings.ToLower(strings.TrimSpace(hex))]; ok {
		return e
	}
	return slackColorFallbackEmoji
}
