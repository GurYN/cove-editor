package lsp

import (
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"
)

// pullClient wires a ready Client to a fake pull-model server over pipes.
func pullClient(t *testing.T, handle func(req request) any) (*Client, chan Event) {
	t.Helper()
	p := newPipes()
	fakeServer(t, p.clientIn, p.serverOut, handle)
	events := make(chan Event, 8)
	c := &Client{lang: "typescript", events: events, state: "ready", pull: true,
		pulling: map[string]bool{}, repull: map[string]bool{}}
	c.conn = newConn(p.clientOut, p.serverIn, c.onNotify, nil)
	return c, events
}

func TestPullDiagnostics(t *testing.T) {
	c, events := pullClient(t, func(req request) any {
		if req.Method != "textDocument/diagnostic" {
			t.Errorf("method = %q", req.Method)
		}
		return map[string]any{"kind": "full", "items": []map[string]any{
			{"range": map[string]any{}, "severity": 1, "message": "boom"},
		}}
	})
	c.maybePull("file:///a.ts")
	select {
	case ev := <-events:
		if ev.Kind != "diagnostics" || len(ev.Diagnostics) != 1 || ev.Diagnostics[0].Message != "boom" {
			t.Fatalf("event = %+v", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no diagnostics event from pull")
	}
}

// A burst of changes mid-pull coalesces into exactly one follow-up pull.
func TestPullCoalescesBursts(t *testing.T) {
	release := make(chan struct{})
	var calls atomic.Int64
	c, events := pullClient(t, func(req request) any {
		if calls.Add(1) == 1 {
			<-release // hold the first pull open while changes pile up
		}
		return map[string]any{"kind": "full", "items": []map[string]any{}}
	})
	uri := "file:///a.ts"
	c.maybePull(uri)
	for range 5 { // burst while the first pull is in flight
		time.Sleep(10 * time.Millisecond)
		c.maybePull(uri)
	}
	close(release)
	for range 2 { // first pull + the one coalesced re-pull
		select {
		case <-events:
		case <-time.After(2 * time.Second):
			t.Fatal("missing pull result")
		}
	}
	time.Sleep(100 * time.Millisecond) // no third pull sneaks in
	if n := calls.Load(); n != 2 {
		t.Fatalf("server saw %d pulls, want 2", n)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.pulling[uri] || c.repull[uri] {
		t.Fatal("pull flags not cleared")
	}
}

// Push-model servers (no diagnosticProvider) must never be pulled.
func TestNoPullForPushServers(t *testing.T) {
	var calls atomic.Int64
	c, _ := pullClient(t, func(req request) any { calls.Add(1); return nil })
	c.pull = false
	c.maybePull("file:///a.go")
	time.Sleep(100 * time.Millisecond)
	if calls.Load() != 0 {
		t.Fatal("pulled a push-model server")
	}
}

// The dynamic registration route flips the pull flag too.
func TestRegisterCapabilityEnablesPull(t *testing.T) {
	c, _ := pullClient(t, func(req request) any { return nil })
	c.pull = false
	params, _ := json.Marshal(map[string]any{"registrations": []map[string]any{
		{"id": "1", "method": "textDocument/diagnostic"},
	}})
	c.onNotify("client/registerCapability", params)
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.pull {
		t.Fatal("registerCapability did not enable pull")
	}
}

func TestTSMajor(t *testing.T) {
	for _, tc := range []struct {
		out  string
		want int
	}{
		{"Version 7.0.2\n", 7}, {"Version 5.9.2", 5}, {"Version 10.1.0", 10},
		{"tsc: bad flag", 0}, {"", 0},
	} {
		if got := tsMajor(tc.out); got != tc.want {
			t.Errorf("tsMajor(%q) = %d, want %d", tc.out, got, tc.want)
		}
	}
}

// A config.toml override must bypass the tsc-version probe entirely.
func TestConfigureOverridesResolve(t *testing.T) {
	saved := defaultServers["typescript"]
	defer func() { defaultServers["typescript"] = saved }()
	Configure("typescript", []string{"my-ts-server", "--stdio"}, nil, "")
	def := defaultServers["typescript"]
	if def.Resolve != nil || def.Argv[0] != "my-ts-server" {
		t.Fatalf("override did not replace the probe: %+v", def)
	}
}
