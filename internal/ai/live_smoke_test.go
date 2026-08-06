package ai

import (
	"context"
	"os"
	"testing"
	"time"
)

// Mirrors the reported miss: cursor on a blank line inside Complete, right
// after the protocol dispatch — the helpers it must NOT re-implement are
// below the cursor, so they ride in the suffix.
const smokePrefix = `func (c *Client) Complete(ctx context.Context, r Request) (string, error) {
	var text string
	var err error
	if c.cfg.Protocol == "anthropic" {
		text, err = c.anthropic(ctx, r)
	} else {
		text, err = c.openai(ctx, r)
	}
	`

const smokeSuffix = `
	return Clean(text), nil
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
	var out struct {
		Choices []struct {
			Message struct {
				Content string ` + "`json:\"content\"`" + `
			} ` + "`json:\"message\"`" + `
		} ` + "`json:\"choices\"`" + `
	}
	if err := c.post(ctx, c.cfg.BaseURL+"/chat/completions", nil, body, &out); err != nil {
		return "", err
	}
	if len(out.Choices) == 0 {
		return "", nil
	}
	return out.Choices[0].Message.Content, nil
}`

func TestLiveOllama(t *testing.T) {
	if os.Getenv("AI_SMOKE") == "" {
		t.Skip("set AI_SMOKE=1 to run against a local Ollama")
	}
	c, err := New(Config{BaseURL: "http://localhost:11434/v1", Model: os.Getenv("AI_SMOKE_MODEL")})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	start := time.Now()
	text, err := c.Complete(ctx, Request{Language: "go", Prefix: smokePrefix, Suffix: smokeSuffix})
	t.Logf("elapsed=%s err=%v\ncompletion=%q", time.Since(start).Round(time.Millisecond), err, text)
	if err != nil {
		t.Fatal(err)
	}
}
