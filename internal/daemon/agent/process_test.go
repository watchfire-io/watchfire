package agent

import (
	"bytes"
	"testing"
)

// TestSubscribeRawFrom_CursorSlice exercises the bytes_received cursor
// math (#0100). The daemon must slice the catch-up snapshot so a
// reconnecting client only receives bytes past its local position —
// otherwise the GUI chat terminal snaps to byte 0 on every re-subscribe.
func TestSubscribeRawFrom_CursorSlice(t *testing.T) {
	tests := []struct {
		name          string
		rawBuf        []byte
		rawTotalBytes int64
		bytesReceived int64
		wantSnapshot  []byte
	}{
		{
			name:          "initial subscribe (cursor=0) returns full buffer",
			rawBuf:        []byte("abcdef"),
			rawTotalBytes: 6,
			bytesReceived: 0,
			wantSnapshot:  []byte("abcdef"),
		},
		{
			name:          "cursor mid-buffer returns tail only",
			rawBuf:        []byte("abcdef"),
			rawTotalBytes: 6,
			bytesReceived: 4,
			wantSnapshot:  []byte("ef"),
		},
		{
			name:          "cursor at end returns empty",
			rawBuf:        []byte("abcdef"),
			rawTotalBytes: 6,
			bytesReceived: 6,
			wantSnapshot:  nil,
		},
		{
			name:          "cursor past end returns empty",
			rawBuf:        []byte("abcdef"),
			rawTotalBytes: 6,
			bytesReceived: 100,
			wantSnapshot:  nil,
		},
		{
			name:          "negative cursor returns full buffer",
			rawBuf:        []byte("abcdef"),
			rawTotalBytes: 6,
			bytesReceived: -5,
			wantSnapshot:  []byte("abcdef"),
		},
		// Buffer has aged out: total broadcast is 1000 but only the
		// last 6 bytes are still in rawBuf (bufStart = 994). A client
		// at cursor 500 missed bytes [500, 994) — daemon sends what
		// it has (full rawBuf). Drift is unavoidable; gap is genuinely
		// lost data.
		{
			name:          "cursor before bufStart returns full buffer",
			rawBuf:        []byte("abcdef"),
			rawTotalBytes: 1000,
			bytesReceived: 500,
			wantSnapshot:  []byte("abcdef"),
		},
		// Cursor inside the still-buffered region after aging.
		// bufStart = 1000 - 6 = 994; cursor at 997 skips 3 bytes.
		{
			name:          "cursor inside aged buffer returns tail",
			rawBuf:        []byte("abcdef"),
			rawTotalBytes: 1000,
			bytesReceived: 997,
			wantSnapshot:  []byte("def"),
		},
		{
			name:          "empty buffer returns empty",
			rawBuf:        nil,
			rawTotalBytes: 0,
			bytesReceived: 0,
			wantSnapshot:  nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := &Process{
				rawSubs:       make(map[string]chan []byte),
				rawBuf:        append([]byte(nil), tc.rawBuf...),
				rawTotalBytes: tc.rawTotalBytes,
			}
			snapshot, ch := p.SubscribeRawFrom("sub-1", tc.bytesReceived)
			defer p.UnsubscribeRaw("sub-1")
			if !bytes.Equal(snapshot, tc.wantSnapshot) {
				t.Fatalf("snapshot mismatch: got %q, want %q", snapshot, tc.wantSnapshot)
			}
			if ch == nil {
				t.Fatalf("expected non-nil live channel")
			}
		})
	}
}

// TestSubscribeRawFrom_LiveBroadcast confirms the subscriber receives
// bytes broadcast *after* the subscribe call, regardless of cursor —
// the catch-up snapshot and the live channel are independent paths.
func TestSubscribeRawFrom_LiveBroadcast(t *testing.T) {
	p := &Process{
		rawSubs:       make(map[string]chan []byte),
		rawBuf:        []byte("history"),
		rawTotalBytes: 7,
	}
	snapshot, ch := p.SubscribeRawFrom("sub-1", 7)
	defer p.UnsubscribeRaw("sub-1")
	if snapshot != nil {
		t.Fatalf("expected nil snapshot when cursor=totalBytes, got %q", snapshot)
	}

	p.broadcastRaw([]byte("live"))

	select {
	case got := <-ch:
		if !bytes.Equal(got, []byte("live")) {
			t.Fatalf("live chunk mismatch: got %q, want %q", got, []byte("live"))
		}
	default:
		t.Fatalf("expected live chunk on channel")
	}
	if p.rawTotalBytes != 11 {
		t.Fatalf("rawTotalBytes should advance: got %d, want 11", p.rawTotalBytes)
	}
}
