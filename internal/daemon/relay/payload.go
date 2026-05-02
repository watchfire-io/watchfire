package relay

// This file pins the canonical Payload schema in its own translation
// unit so future spec changes (new fields, version bump, deprecation
// markers) land in one place. The struct + builder definitions live in
// `relay.go` for historical reasons (the Slack + Discord adapters from
// tasks 0063/0064 imported them from there); this file documents the
// JSON shape so reviewers don't have to reverse-engineer it from the
// adapter call sites.
//
// Wire shape — pinned at version 1:
//
//   {
//     "version":             1,
//     "kind":                "TASK_FAILED" | "RUN_COMPLETE" | "WEEKLY_DIGEST",
//     "emitted_at":          "2026-05-02T09:30:00Z",
//     "project_id":          "<uuid>",
//     "project_name":        "<display>",
//     "project_color":       "#3b82f6",
//     "task_number":         42,
//     "task_title":          "<task title>",
//     "task_failure_reason": "<reason>",        // TASK_FAILED only
//     "deep_link":           "watchfire://...", // task or digest
//     "digest_date":         "2026-05-02",      // WEEKLY_DIGEST only
//     "digest_path":         "/.../digests/<date>.md",
//     "digest_body":         "<rendered markdown>"
//   }
//
// Receivers MUST ignore unknown fields. Adding a new field is a minor
// compatibility bump (Version stays at 1); removing or repurposing one
// requires a Version=2 cutover so receivers can branch on the envelope.
//
// The dispatcher (dispatcher.go) is the single producer of Payloads on
// the live emit path; the integrations service's TestIntegration RPC
// produces synthetic payloads directly for the channel-preview path.
