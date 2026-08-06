package ai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOpenAIProtocol(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if h := r.Header.Get("Authorization"); h != "Bearer sk-test" {
			t.Errorf("auth = %q", h)
		}
		json.NewDecoder(r.Body).Decode(&got)
		w.Write([]byte(`{"choices":[{"message":{"content":"return nil\n"}}]}`))
	}))
	defer srv.Close()

	c, err := New(Config{BaseURL: srv.URL + "/v1", Model: "qwen2.5-coder", Key: "sk-test"})
	if err != nil {
		t.Fatal(err)
	}
	text, err := c.Complete(context.Background(), Request{Language: "go", Prefix: "func f() error {\n\t", Suffix: "\n}"})
	if err != nil {
		t.Fatal(err)
	}
	if text != "return nil" {
		t.Errorf("text = %q", text)
	}
	if got["model"] != "qwen2.5-coder" || got["stream"] != false {
		t.Errorf("body = %v", got)
	}
	msgs := got["messages"].([]any)
	if len(msgs) != 2 {
		t.Fatalf("messages = %d", len(msgs))
	}
	user := msgs[1].(map[string]any)["content"].(string)
	if want := "func f() error {\n\t<CURSOR>\n}"; !contains(user, want) {
		t.Errorf("user prompt missing cursor window: %q", user)
	}
}

func TestAnthropicProtocol(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if h := r.Header.Get("x-api-key"); h != "sk-ant" {
			t.Errorf("key = %q", h)
		}
		if h := r.Header.Get("anthropic-version"); h != "2023-06-01" {
			t.Errorf("version = %q", h)
		}
		json.NewDecoder(r.Body).Decode(&got)
		w.Write([]byte(`{"content":[{"type":"text","text":"x := 1"}]}`))
	}))
	defer srv.Close()

	c, err := New(Config{Protocol: "anthropic", BaseURL: srv.URL, Model: "claude-haiku-4-5", Key: "sk-ant"})
	if err != nil {
		t.Fatal(err)
	}
	text, err := c.Complete(context.Background(), Request{Language: "go", Prefix: "a"})
	if err != nil {
		t.Fatal(err)
	}
	if text != "x := 1" {
		t.Errorf("text = %q", text)
	}
	if got["system"] == "" || got["model"] != "claude-haiku-4-5" {
		t.Errorf("body = %v", got)
	}
}

func TestServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		w.Write([]byte(`{"error":{"message":"invalid api key"}}`))
	}))
	defer srv.Close()
	c, _ := New(Config{BaseURL: srv.URL, Model: "m"})
	_, err := c.Complete(context.Background(), Request{})
	if err == nil || !contains(err.Error(), "invalid api key") {
		t.Errorf("err = %v", err)
	}
}

func TestConfigValidation(t *testing.T) {
	if _, err := New(Config{BaseURL: "http://x", Model: ""}); err == nil {
		t.Error("missing model accepted")
	}
	if _, err := New(Config{Model: "m"}); err == nil {
		t.Error("openai without base_url accepted")
	}
	if _, err := New(Config{Protocol: "anthropic", Model: "m"}); err != nil {
		t.Errorf("anthropic default base_url rejected: %v", err)
	}
	if _, err := New(Config{Protocol: "grpc", BaseURL: "http://x", Model: "m"}); err == nil {
		t.Error("unknown protocol accepted")
	}
}

func TestClean(t *testing.T) {
	cases := [][2]string{
		{"return nil\n", "return nil"},
		{"```go\nreturn nil\n```", "return nil"},
		{"```\nx\n```\n", "x"},
		{"   ", ""},
		{"```", ""},
		{"plain", "plain"},
		{"<CURSOR>t.state = done", "t.state = done"},  // marker echoed at the front
		{"t.handleSTR()\n<CUURSOR>", "t.handleSTR()"}, // misspelled echo (small models)
		{"x</cursor>", "x"},                           // closing-tag variant
		{"<CURSOR>", ""},                              // marker-only response
		{"return c\n```", "return c"},                 // trailing fence, no leading one
		{"x\n``` ", "x"},                              // trailing fence with stray space
		{"``````", ""},                                // backticks only
	}
	for _, c := range cases {
		if got := Clean(c[0]); got != c[1] {
			t.Errorf("Clean(%q) = %q, want %q", c[0], got, c[1])
		}
	}
}

func TestTrimSuffixOverlap(t *testing.T) {
	cases := []struct{ s, suffix, want string }{
		// ramble past the insertion point into existing code: cut there
		{"if err != nil {\n\treturn err\n}\nreturn Clean(text), nil\nmore", "\n\treturn Clean(text), nil\n}", "if err != nil {\n\treturn err\n}"},
		// suggestion IS the next line: no-op, drop entirely
		{"return Clean(text), nil", "\n\treturn Clean(text), nil\n}", ""},
		// no overlap: untouched
		{"x := 1", "\nreturn x\n", "x := 1"},
		// empty suffix: untouched
		{"x := 1", "", "x := 1"},
		// bare closer below the cursor must not truncate blocks
		{"if a {\n\tb()\n}", "\n}\n}", "if a {\n\tb()\n}"},
	}
	for _, c := range cases {
		if got := trimSuffixOverlap(c.s, c.suffix); got != c.want {
			t.Errorf("trim(%q, %q) = %q, want %q", c.s, c.suffix, got, c.want)
		}
	}
}

func contains(s, sub string) bool { return strings.Contains(s, sub) }
