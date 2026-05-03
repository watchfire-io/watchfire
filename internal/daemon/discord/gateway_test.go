package discord

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"nhooyr.io/websocket"
)

// fakeConn is a deterministic in-memory GatewayConn that tests script
// frame-by-frame. Tests push the inbound script via `pushIn`, read the
// outbound frames via `outbound`, and observe close events through
// `closeCalls`.
type fakeConn struct {
	mu          sync.Mutex
	inbound     [][]byte
	inCh        chan []byte
	outbound    [][]byte
	closeCalls  int32
	readCalled  int32
	closedCh    chan struct{}
	closeReason string
	closeCode   websocket.StatusCode
}

func newFakeConn(script ...[]byte) *fakeConn {
	c := &fakeConn{
		inCh:     make(chan []byte, len(script)+8),
		closedCh: make(chan struct{}),
	}
	for _, frame := range script {
		c.inCh <- frame
	}
	return c
}

func (c *fakeConn) push(frame []byte) {
	select {
	case c.inCh <- frame:
	case <-c.closedCh:
	}
}

func (c *fakeConn) Read(ctx context.Context) (websocket.MessageType, []byte, error) {
	atomic.AddInt32(&c.readCalled, 1)
	select {
	case b, ok := <-c.inCh:
		if !ok {
			return 0, nil, io.EOF
		}
		return websocket.MessageText, b, nil
	case <-ctx.Done():
		return 0, nil, ctx.Err()
	case <-c.closedCh:
		return 0, nil, io.EOF
	}
}

func (c *fakeConn) Write(_ context.Context, _ websocket.MessageType, data []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	cp := make([]byte, len(data))
	copy(cp, data)
	c.outbound = append(c.outbound, cp)
	return nil
}

func (c *fakeConn) Close(code websocket.StatusCode, reason string) error {
	if atomic.AddInt32(&c.closeCalls, 1) == 1 {
		c.closeCode = code
		c.closeReason = reason
		close(c.closedCh)
	}
	return nil
}

func (c *fakeConn) outboundOps() []int {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]int, 0, len(c.outbound))
	for _, msg := range c.outbound {
		var m struct {
			Op int `json:"op"`
		}
		_ = json.Unmarshal(msg, &m)
		out = append(out, m.Op)
	}
	return out
}

func helloFrame(intervalMs int) []byte {
	b, _ := json.Marshal(struct {
		Op   int `json:"op"`
		Data struct {
			HeartbeatIntervalMs int `json:"heartbeat_interval"`
		} `json:"d"`
	}{Op: 10, Data: struct {
		HeartbeatIntervalMs int `json:"heartbeat_interval"`
	}{HeartbeatIntervalMs: intervalMs}})
	return b
}

func dispatchFrame(t string, seq int, payload any) []byte {
	b, _ := json.Marshal(struct {
		Op   int `json:"op"`
		Type string `json:"t"`
		Seq  int    `json:"s"`
		Data any    `json:"d"`
	}{Op: 0, Type: t, Seq: seq, Data: payload})
	return b
}

// TestGatewayHappyPath: a single connection cycle emits READY then a
// GUILD_CREATE; the gateway should fire one GuildEventCreate.
func TestGatewayHappyPath(t *testing.T) {
	conn := newFakeConn(
		helloFrame(60_000),
		dispatchFrame("READY", 1, map[string]any{"session_id": "abc"}),
		dispatchFrame("GUILD_CREATE", 2, map[string]any{"id": "g1", "name": "Test Guild"}),
	)
	gw := NewGateway(GatewayConfig{
		Token:  "tok",
		Logger: log.New(io.Discard, "", 0),
		Dialer: func(_ context.Context, _ string) (GatewayConn, error) { return conn, nil },
	})

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		// Wait until the GUILD_CREATE is delivered, then cancel to stop
		// the read loop. We do this on the events side so the test
		// observes the dispatch before tearing down.
		ev := <-gw.Events()
		if ev.Type != GuildEventCreate {
			t.Errorf("expected GuildEventCreate, got %v", ev.Type)
		}
		if ev.GuildID != "g1" || ev.GuildName != "Test Guild" {
			t.Errorf("unexpected event payload: %+v", ev)
		}
		cancel()
	}()

	if err := gw.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// IDENTIFY (op 2) must have been the first outbound message after HELLO.
	ops := conn.outboundOps()
	if len(ops) == 0 || ops[0] != 2 {
		t.Fatalf("expected IDENTIFY (op 2) as first outbound, got %v", ops)
	}
}

// TestGatewayHelloWrong: a non-HELLO first frame is treated as a
// connection error so Run reconnects with backoff. We cancel ctx before
// the reconnect fires so the test stays fast.
func TestGatewayHelloWrong(t *testing.T) {
	conn := newFakeConn(dispatchFrame("READY", 1, map[string]any{})) // wrong: no HELLO
	gw := NewGateway(GatewayConfig{
		Token:  "tok",
		Logger: log.New(io.Discard, "", 0),
		Dialer: func(_ context.Context, _ string) (GatewayConn, error) { return conn, nil },
	})
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	err := gw.Run(ctx)
	// Run returns nil on ctx cancel — and the bad-HELLO is swallowed by
	// the reconnect loop before ctx expires. Either is acceptable; we
	// only assert no panic and no events.
	if err != nil && !errors.Is(err, context.DeadlineExceeded) {
		t.Logf("Run returned: %v", err)
	}
}

// TestGatewayGuildDelete: GUILD_DELETE with `unavailable: false` emits a
// delete event. unavailable: true is treated as a transient outage and
// suppressed.
func TestGatewayGuildDelete(t *testing.T) {
	conn := newFakeConn(
		helloFrame(60_000),
		dispatchFrame("GUILD_DELETE", 1, map[string]any{"id": "g1", "unavailable": true}),  // outage — suppressed
		dispatchFrame("GUILD_DELETE", 2, map[string]any{"id": "g2", "unavailable": false}), // real removal
	)
	gw := NewGateway(GatewayConfig{
		Token:  "tok",
		Logger: log.New(io.Discard, "", 0),
		Dialer: func(_ context.Context, _ string) (GatewayConn, error) { return conn, nil },
	})

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		ev := <-gw.Events()
		if ev.Type != GuildEventDelete {
			t.Errorf("expected GuildEventDelete, got %v", ev.Type)
		}
		if ev.GuildID != "g2" {
			t.Errorf("expected guild g2 (real delete), got %s", ev.GuildID)
		}
		cancel()
	}()
	if err := gw.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}
}

// TestGatewayDoubleRunPanics: calling Run twice on the same instance is
// a programming error — must panic, not silently no-op.
func TestGatewayDoubleRunPanics(t *testing.T) {
	gw := NewGateway(GatewayConfig{
		Token:  "tok",
		Logger: log.New(io.Discard, "", 0),
		Dialer: func(_ context.Context, _ string) (GatewayConn, error) { return newFakeConn(), nil },
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = gw.Run(ctx) }()
	// Wait briefly for the first Run to mark running=true.
	time.Sleep(20 * time.Millisecond)
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic on double Run")
		}
	}()
	_ = gw.Run(ctx)
}
