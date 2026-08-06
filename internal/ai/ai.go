// Package ai provides inline code completion over a configurable HTTP
// backend. Two wire protocols cover the field: "openai" (chat/completions —
// Ollama, LM Studio, llama.cpp, OpenRouter, Groq, Mistral, vLLM, …) and
// "anthropic" (the native Messages API). No SDK dependencies: both bodies
// are a handful of JSON fields, stdlib net/http is plenty.
package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync/atomic"
	"time"
)

// Config selects the backend. Key may be empty (local runtimes like Ollama
// need none).
type Config struct {
	Protocol  string // "openai" (default) | "anthropic"
	BaseURL   string // required for openai; defaults to api.anthropic.com for anthropic
	Model     string // required
	Key       string
	MaxTokens int // default 256
}

// Request is one completion ask: the code around the cursor.
type Request struct {
	Language string // language id or file extension, "" if unknown
	Prefix   string // code before the cursor
	Suffix   string // code after the cursor
}

type Client struct {
	cfg    Config
	http   *http.Client
	noTemp atomic.Bool // provider rejected "temperature": skip it from now on
}

// New validates cfg and returns a client. Errors are configuration
// problems the user must fix (surfaced as startup warnings).
func New(cfg Config) (*Client, error) {
	switch cfg.Protocol {
	case "", "openai":
		cfg.Protocol = "openai"
	case "anthropic":
	default:
		return nil, fmt.Errorf("ai: unknown protocol %q (use \"openai\" or \"anthropic\")", cfg.Protocol)
	}
	if cfg.Model == "" {
		return nil, errors.New("ai: model is required")
	}
	if cfg.BaseURL == "" {
		if cfg.Protocol != "anthropic" {
			return nil, errors.New("ai: base_url is required for the openai protocol")
		}
		cfg.BaseURL = "https://api.anthropic.com"
	}
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	if cfg.MaxTokens <= 0 {
		// Generous on purpose: reasoning models (kimi, qwen3, …) burn hidden
		// "thinking" tokens from the same budget before emitting content —
		// 256 often returns an empty suggestion. Plain models stop early, so
		// the headroom costs nothing there.
		cfg.MaxTokens = 2048
	}
	return &Client{cfg: cfg, http: &http.Client{Timeout: 30 * time.Second}}, nil
}

const systemPrompt = "You are a code completion engine inside a text editor. " +
	"Output only the exact text to insert at <CURSOR> — no explanations, no markdown fences, " +
	"no repetition of the surrounding code, and never the literal token <CURSOR> itself. " +
	"Call functions and helpers that already exist in the surrounding code instead of " +
	"re-implementing them. Match the file's indentation style. " +
	"If nothing useful can be inserted, output nothing."

func userPrompt(r Request) string {
	lang := r.Language
	if lang == "" {
		lang = "unknown"
	}
	return fmt.Sprintf("Language: %s\n\n<code>\n%s<CURSOR>%s\n</code>", lang, r.Prefix, r.Suffix)
}

// Complete asks the backend for the text to insert at the cursor. The
// result is cleaned (fences stripped, trailing whitespace dropped); "" means
// no suggestion.
func (c *Client) Complete(ctx context.Context, r Request) (string, error) {
	var text string
	var err error
	if c.cfg.Protocol == "anthropic" {
		text, err = c.anthropic(ctx, r)
	} else {
		text, err = c.openai(ctx, r)
	}

	if err != nil {
		logf("--- request (%s, %s)\n%s\n--- error\n%v\n\n", c.cfg.Model, r.Language, userPrompt(r), err)
		return "", err
	}
	out := trimSuffixOverlap(Clean(text), r.Suffix)
	logf("--- request (%s, %s)\n%s\n--- raw response\n%q\n--- cleaned\n%q\n\n", c.cfg.Model, r.Language, userPrompt(r), text, out)
	return out, nil
}

// trimSuffixOverlap cuts a suggestion at the first line that duplicates the
// first non-blank line already below the cursor — models love to re-emit the
// code that follows (or ramble on past the insertion point into it). A
// suggestion whose first line is that duplicate is a no-op and drops to "".
func trimSuffixOverlap(s, suffix string) string {
	next := ""
	for _, ln := range strings.Split(suffix, "\n") {
		if t := strings.TrimSpace(ln); t != "" {
			next = t
			break
		}
	}
	// Trivial lines (a bare "}", "})", …) sit below almost every cursor and
	// legitimately appear inside suggestions — only trim on distinctive ones.
	if len(next) < 4 || s == "" {
		return s
	}
	lines := strings.Split(s, "\n")
	for i, ln := range lines {
		if strings.TrimSpace(ln) == next {
			return strings.TrimRight(strings.Join(lines[:i], "\n"), " \t\n")
		}
	}
	return s
}

// logf appends prompt/response pairs to the file named by $COVE_AI_LOG —
// the "is the model seeing the right context?" debugging hook. No-op when
// the variable is unset.
func logf(format string, a ...any) {
	p := os.Getenv("COVE_AI_LOG")
	if p == "" {
		return
	}
	f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, format, a...)
}

func (c *Client) openai(ctx context.Context, r Request) (string, error) {
	body := map[string]any{
		"model": c.cfg.Model,
		"messages": []map[string]string{
			{"role": "system", "content": systemPrompt},
			{"role": "user", "content": userPrompt(r)},
		},
		"max_tokens": c.cfg.MaxTokens,
		"stream":     false,
	}
	// Greedy decode: completion wants the most probable continuation, not
	// creative variety — it noticeably steadies small local models.
	if !c.noTemp.Load() {
		body["temperature"] = 0
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	hdr := map[string]string{}
	if c.cfg.Key != "" {
		hdr["Authorization"] = "Bearer " + c.cfg.Key
	}
	err := c.post(ctx, c.cfg.BaseURL+"/chat/completions", hdr, body, &out)
	if err != nil && strings.Contains(err.Error(), "temperature") {
		// Best-effort: some providers pin temperature (reasoning models that
		// only allow 1) — retry without it, and remember for later requests.
		c.noTemp.Store(true)
		delete(body, "temperature")
		err = c.post(ctx, c.cfg.BaseURL+"/chat/completions", hdr, body, &out)
	}
	if err != nil {
		return "", err
	}
	if len(out.Choices) == 0 {
		return "", nil
	}
	return out.Choices[0].Message.Content, nil
}

func (c *Client) anthropic(ctx context.Context, r Request) (string, error) {
	body := map[string]any{
		"model":      c.cfg.Model,
		"max_tokens": c.cfg.MaxTokens,
		"system":     systemPrompt,
		"messages": []map[string]string{
			{"role": "user", "content": userPrompt(r)},
		},
	}
	var out struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	hdr := map[string]string{"anthropic-version": "2023-06-01"}
	if c.cfg.Key != "" {
		hdr["x-api-key"] = c.cfg.Key
	}
	if err := c.post(ctx, c.cfg.BaseURL+"/v1/messages", hdr, body, &out); err != nil {
		return "", err
	}
	for _, b := range out.Content {
		if b.Type == "text" {
			return b.Text, nil
		}
	}
	return "", nil
}

func (c *Client) post(ctx context.Context, url string, hdr map[string]string, body, out any) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%s: %s", resp.Status, errSnippet(data))
	}
	return json.Unmarshal(data, out)
}

// errSnippet pulls the server's error message out of the response body —
// both protocols nest it under {"error": {"message": ...}} — falling back
// to a truncated raw body.
func errSnippet(data []byte) string {
	var e struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(data, &e) == nil && e.Error.Message != "" {
		data = []byte(e.Error.Message)
	}
	s := strings.TrimSpace(string(data))
	if len(s) > 120 {
		s = s[:117] + "…"
	}
	return s
}

// cursorEcho matches the prompt's <CURSOR> marker echoed back by the model,
// including the misspelled variants small models produce (<CUURSOR>, </cursor>).
// ponytail: also eats a legit angle-bracketed Cursor type in generated code;
// tighten if that ever bites.
var cursorEcho = regexp.MustCompile(`(?i)<[^>\n]*c+u+r+s+o+r+[^>\n]*>`)

// Clean normalizes a model response into insertable text: markdown fences
// and cursor-marker echoes stripped, trailing whitespace dropped. "" means
// no suggestion.
func Clean(s string) string {
	s = cursorEcho.ReplaceAllString(s, "")
	s = strings.TrimRight(s, " \t\n")
	if strings.HasPrefix(s, "```") {
		if i := strings.IndexByte(s, '\n'); i >= 0 {
			s = s[i+1:]
		} else {
			return ""
		}
		s = strings.TrimRight(s, " \t\n")
	}
	// A trailing fence shows up with or without a leading one ("return c\n```").
	if ln := strings.LastIndexByte(s, '\n'); true {
		if last := strings.TrimSpace(s[ln+1:]); last != "" && strings.Trim(last, "`") == "" {
			if ln < 0 {
				return ""
			}
			s = strings.TrimRight(s[:ln], " \t\n")
		}
	}
	return s
}
