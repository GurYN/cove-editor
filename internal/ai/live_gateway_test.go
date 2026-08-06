package ai

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestLiveGateway(t *testing.T) {
	key := os.Getenv("AI_GW_KEY")
	if key == "" {
		t.Skip("set AI_GW_KEY to run against the opencode gateway")
	}
	c, err := New(Config{BaseURL: "https://opencode.ai/zen/go/v1", Model: "kimi-k2.7-code", Key: key})
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
