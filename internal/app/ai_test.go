package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/GurYN/cove-editor/internal/ai"
)

// fakeCompleter records the request and returns a canned suggestion.
type fakeCompleter struct {
	req  ai.Request
	text string
	err  error
}

func (f *fakeCompleter) Complete(_ context.Context, r ai.Request) (string, error) {
	f.req = r
	return f.text, f.err
}

// aiModel builds an app with one Go file open and a stubbed AI backend.
func aiModel(t *testing.T, src string) (Model, *fakeCompleter) {
	t.Helper()
	t.Setenv("COVE_CONFIG", filepath.Join(t.TempDir(), "config.toml"))
	path := filepath.Join(t.TempDir(), "main.go")
	os.WriteFile(path, []byte(src), 0o644)
	m := New(path, []byte(src))
	m.width, m.height = 100, 30
	m.layout()
	fake := &fakeCompleter{text: "return nil"}
	m.ai.client = fake
	m.ai.enabled = true
	return m, fake
}

// runAI drives the schedule → tick → request → result pipeline synchronously.
func runAI(m *Model) {
	m.scheduleAI() // arms the gen; the tick itself is time-based, fake it
	if cmd := m.aiRequest(m.ai.gen, false); cmd != nil {
		if msg, ok := cmd().(aiComplMsg); ok {
			m.handleAIResult(msg)
		}
	}
}

func TestAIGhostRoundTrip(t *testing.T) {
	m, fake := aiModel(t, "func f() error {\n\t\n}\n")
	d := m.doc()
	d.ed.Go(1, 1) // end of the blank body line

	runAI(&m)
	if !d.ed.GhostVisible() {
		t.Fatal("ghost not visible after result")
	}
	if !strings.Contains(fake.req.Prefix, "func f() error {") || fake.req.Language != "go" {
		t.Errorf("request = %+v", fake.req)
	}

	// Tab accepts.
	mm, _ := m.dispatchKey(tea.KeyMsg{Type: tea.KeyTab})
	if got := string(mm.doc().ed.Buf.Bytes()); !strings.Contains(got, "\treturn nil\n}") {
		t.Errorf("buffer after accept = %q", got)
	}
	if mm.doc().ed.Ghost != "" {
		t.Error("ghost not cleared by accept")
	}
}

func TestAIGhostEscDismisses(t *testing.T) {
	m, _ := aiModel(t, "x\n")
	d := m.doc()
	d.ed.Go(0, 1)
	runAI(&m)
	if !d.ed.GhostVisible() {
		t.Fatal("ghost not visible")
	}
	mm, _ := m.dispatchKey(tea.KeyMsg{Type: tea.KeyEscape})
	if mm.doc().ed.Ghost != "" {
		t.Error("Esc did not dismiss ghost")
	}
	if got := string(mm.doc().ed.Buf.Bytes()); got != "x\n" {
		t.Errorf("Esc changed the buffer: %q", got)
	}
}

func TestAIStaleResultDropped(t *testing.T) {
	m, _ := aiModel(t, "x\n")
	d := m.doc()
	d.ed.Go(0, 1)
	m.scheduleAI()
	cmd := m.aiRequest(m.ai.gen, false)
	d.ed.InsertText("y") // buffer moves while the request is in flight
	if msg, ok := cmd().(aiComplMsg); ok {
		m.handleAIResult(msg)
	}
	if d.ed.Ghost != "" {
		t.Error("stale result installed a ghost")
	}
}

func TestAIMidLineNoRequest(t *testing.T) {
	m, _ := aiModel(t, "abcdef\n")
	m.doc().ed.Go(0, 3)
	m.scheduleAI()
	if cmd := m.aiRequest(m.ai.gen, false); cmd != nil {
		t.Error("mid-line cursor fired a request")
	}
}

func TestAIErrorSilentInBackground(t *testing.T) {
	m, fake := aiModel(t, "x\n")
	fake.err = errors.New("boom")
	m.doc().ed.Go(0, 1)
	runAI(&m)
	if m.msgToast {
		t.Errorf("background error surfaced a toast: %q", m.lastMsg)
	}
	// Manual trigger surfaces it.
	m.ai.gen++
	if cmd := m.aiRequest(m.ai.gen, true); cmd != nil {
		if msg, ok := cmd().(aiComplMsg); ok {
			m.handleAIResult(msg)
		}
	}
	if !m.msgErr || !strings.Contains(m.lastMsg, "boom") {
		t.Errorf("manual error not surfaced: %q", m.lastMsg)
	}
}

func TestAIGhostLengthCap(t *testing.T) {
	m, fake := aiModel(t, "x\n")
	fake.text = strings.Repeat("line\n", 40) + "line"
	d := m.doc()
	d.ed.Go(0, 1)
	runAI(&m)
	if !d.ed.GhostVisible() {
		t.Fatal("ghost not visible")
	}
	if got := strings.Count(d.ed.Ghost, "\n") + 1; got != aiMaxGhostLines {
		t.Errorf("ghost lines = %d, want %d", got, aiMaxGhostLines)
	}
}

func TestAIStatusSegment(t *testing.T) {
	m, _ := aiModel(t, "x\n")
	if got := m.aiSeg(); !strings.Contains(got, "ai✓") {
		t.Errorf("idle seg = %q", got)
	}
	m.doc().ed.Go(0, 1)
	m.scheduleAI()
	cmd := m.aiRequest(m.ai.gen, false)
	if got := m.aiSeg(); !strings.Contains(got, "ai…") {
		t.Errorf("in-flight seg = %q", got)
	}
	if msg, ok := cmd().(aiComplMsg); ok {
		m.handleAIResult(msg)
	}
	if got := m.aiSeg(); !strings.Contains(got, "ai✓") {
		t.Errorf("post-result seg = %q", got)
	}
	m.ai.enabled = false
	if got := m.aiSeg(); !strings.Contains(got, "ai·") {
		t.Errorf("disabled seg = %q", got)
	}
	m.ai.client = nil
	if got := m.aiSeg(); got != "" {
		t.Errorf("unconfigured seg = %q", got)
	}
}

func TestAIConfigWiring(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.toml")
	os.WriteFile(cfgPath, []byte("[ai]\nenabled = true\nbase_url = \"http://localhost:11434/v1\"\nmodel = \"qwen2.5-coder\"\ndebounce_ms = 500\n"), 0o644)
	t.Setenv("COVE_CONFIG", cfgPath)
	m := New(t.TempDir(), nil)
	if m.ai.client == nil || !m.ai.enabled {
		t.Fatalf("ai not configured; warns = %v", m.cfgWarns)
	}
	if m.ai.debounce.Milliseconds() != 500 {
		t.Errorf("debounce = %v", m.ai.debounce)
	}
	if m.reg.ByID("ai.toggle") == nil || m.reg.ByID("ai.trigger") == nil {
		t.Error("ai actions not registered")
	}
}

func TestAIManualModeNoAutoSchedule(t *testing.T) {
	m, _ := aiModel(t, "func f() error {\n\t\n}\n")
	m.doc().ed.Go(1, 1)
	m.ai.manual = true
	if m.scheduleAI() != nil {
		t.Error("manual mode still auto-schedules")
	}
	// Explicit trigger (what alt+\ dispatches) must still work.
	m.ai.gen++
	if m.aiRequest(m.ai.gen, true) == nil {
		t.Error("manual trigger produced no request")
	}
}

func TestAIConfigMissingModelWarns(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.toml")
	os.WriteFile(cfgPath, []byte("[ai]\nenabled = true\nbase_url = \"http://x\"\n"), 0o644)
	t.Setenv("COVE_CONFIG", cfgPath)
	m := New(t.TempDir(), nil)
	if m.ai.client != nil {
		t.Error("broken config produced a client")
	}
	if !strings.Contains(strings.Join(m.cfgWarns, ";"), "model") {
		t.Errorf("no warning, cfgWarns = %v", m.cfgWarns)
	}
}
