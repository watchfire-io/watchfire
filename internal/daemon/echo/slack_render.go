package echo

import (
	"encoding/json"
)

// RenderSlack converts a transport-agnostic CommandResponse into the
// JSON wire format Slack expects in the synchronous response body to a
// slash command or block_actions interaction.
//
// Translation rules (mirrors `commands.go::Block` + the Discord renderer):
//
//   - "header"   → Block Kit `header` block with `plain_text`.
//   - "section"  → Block Kit `section` block with `plain_text` (or
//                  `mrkdwn` when Block.Markdown is true).
//   - "context"  → Block Kit `context` block with one `mrkdwn` element.
//   - "divider"  → Block Kit `divider` block.
//   - resp.Ephemeral → `response_type: "ephemeral"`.
//   - resp.InChannel → `response_type: "in_channel"`. Defaults to
//     ephemeral when neither flag is set so a misfired button click
//     never leaks state to a public channel.
//
// The output is the raw JSON the handler writes verbatim. A nil resp
// produces an empty-content ephemeral so the transport never has to
// special-case nil.
func RenderSlack(resp *CommandResponse) []byte {
	if resp == nil {
		return []byte(`{"response_type":"ephemeral","text":""}`)
	}

	out := map[string]any{}
	switch {
	case resp.InChannel:
		out["response_type"] = "in_channel"
	default:
		// Default to ephemeral on any "neither flag set" case — buttons
		// fired in a public channel should not leak status text to the
		// channel unless the handler explicitly opts in.
		out["response_type"] = "ephemeral"
	}
	if resp.Text != "" {
		out["text"] = resp.Text
	}
	if blocks := blocksToSlackBlocks(resp.Blocks); len(blocks) > 0 {
		out["blocks"] = blocks
	}
	body, err := json.Marshal(out)
	if err != nil {
		return []byte(`{"response_type":"ephemeral","text":""}`)
	}
	return body
}

// RenderSlackUpdate is the variant used to replace the original message
// (`replace_original: true`) in response to a button click. Slack accepts
// the same Block Kit shape as RenderSlack but with the replace flag set
// on the envelope. Used by `watchfire_retry` button clicks so the
// failure ping mutates into a "retrying" confirmation in place rather
// than spawning an ephemeral DM next to it.
func RenderSlackUpdate(resp *CommandResponse) []byte {
	if resp == nil {
		return []byte(`{"replace_original":true,"text":""}`)
	}
	out := map[string]any{
		"replace_original": true,
	}
	if resp.Text != "" {
		out["text"] = resp.Text
	}
	if blocks := blocksToSlackBlocks(resp.Blocks); len(blocks) > 0 {
		out["blocks"] = blocks
	}
	body, err := json.Marshal(out)
	if err != nil {
		return []byte(`{"replace_original":true,"text":""}`)
	}
	return body
}

// blocksToSlackBlocks converts the transport-agnostic Block slice into
// Slack's Block Kit shape. Mirrors `discord_render.go::blocksToEmbeds`
// but for Slack — Slack supports the full Block Kit, so the translation
// is much closer to 1:1 than Discord's lossy embed mapping.
func blocksToSlackBlocks(blocks []Block) []map[string]any {
	if len(blocks) == 0 {
		return nil
	}
	out := make([]map[string]any, 0, len(blocks))
	for _, b := range blocks {
		switch b.Type {
		case "header":
			if b.Text == "" {
				continue
			}
			out = append(out, map[string]any{
				"type": "header",
				"text": map[string]any{
					"type":  "plain_text",
					"text":  b.Text,
					"emoji": true,
				},
			})
		case "section":
			if b.Text == "" {
				continue
			}
			textType := "plain_text"
			if b.Markdown {
				textType = "mrkdwn"
			}
			textObj := map[string]any{
				"type": textType,
				"text": b.Text,
			}
			if textType == "plain_text" {
				textObj["emoji"] = true
			}
			out = append(out, map[string]any{
				"type": "section",
				"text": textObj,
			})
		case "context":
			if b.Text == "" {
				continue
			}
			out = append(out, map[string]any{
				"type": "context",
				"elements": []map[string]any{{
					"type": "mrkdwn",
					"text": b.Text,
				}},
			})
		case "divider":
			out = append(out, map[string]any{"type": "divider"})
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// CancelReasonModalView returns the Block Kit JSON for the Slack modal
// surfaced when a user clicks the Cancel button. The modal's
// `private_metadata` carries `<projectID>|<taskNumber>` so the
// view_submission handler can route the cancellation back to the right
// task without re-resolving it from a button value (which Slack does
// not echo into view_submission payloads).
//
// `views.open` is the Slack API call the handler makes synchronously
// against the trigger_id; this function only renders the view block —
// the HTTP transport lives in `handler_slack.go::openCancelModal`.
func CancelReasonModalView(projectID string, taskNumber int, taskTitle string) map[string]any {
	private := joinProjectTaskRef(projectID, taskNumber)
	headline := "Cancel this task?"
	if taskTitle != "" {
		headline = "Cancel: " + taskTitle
	}
	return map[string]any{
		"type":             "modal",
		"callback_id":      cancelModalCallbackID,
		"private_metadata": private,
		"title": map[string]any{
			"type":  "plain_text",
			"text":  "Cancel task",
			"emoji": true,
		},
		"submit": map[string]any{
			"type":  "plain_text",
			"text":  "Cancel task",
			"emoji": true,
		},
		"close": map[string]any{
			"type":  "plain_text",
			"text":  "Keep running",
			"emoji": true,
		},
		"blocks": []map[string]any{
			{
				"type": "section",
				"text": map[string]any{
					"type": "mrkdwn",
					"text": "*" + headline + "*",
				},
			},
			{
				"type":     "input",
				"block_id": cancelModalReasonBlock,
				"label": map[string]any{
					"type":  "plain_text",
					"text":  "Why are you cancelling?",
					"emoji": true,
				},
				"element": map[string]any{
					"type":      "plain_text_input",
					"action_id": cancelModalReasonAction,
					"multiline": true,
					"placeholder": map[string]any{
						"type": "plain_text",
						"text": "Optional — surfaces in failure_reason on the task YAML.",
					},
				},
				"optional": true,
			},
		},
	}
}
