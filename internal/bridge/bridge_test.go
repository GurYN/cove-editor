package bridge

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

type fakeHandler struct{}

func (fakeHandler) Tools() []Tool {
	return []Tool{{Name: "echo", Description: "echoes", InputSchema: json.RawMessage(`{"type":"object"}`)}}
}

func (fakeHandler) Call(ctx context.Context, name string, args json.RawMessage) (any, error) {
	switch name {
	case "echo":
		var a struct{ Text string }
		json.Unmarshal(args, &a)
		return map[string]string{"got": a.Text}, nil
	case "slow":
		select {
		case <-time.After(300 * time.Millisecond):
			return "done", nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return nil, errors.New("no such tool: " + name)
}

// session drives Serve over pipes and returns a send/recv pair.
func session(t *testing.T, h Handler) (func(string), func() map[string]any) {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	go func() { Serve(context.Background(), inR, outW, h, "test"); outW.Close() }()
	t.Cleanup(func() { inW.Close() })
	br := bufio.NewReader(outR)
	send := func(s string) { inW.Write([]byte(s + "\n")) }
	recv := func() map[string]any {
		line, err := br.ReadBytes('\n')
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		var m map[string]any
		if err := json.Unmarshal(line, &m); err != nil {
			t.Fatalf("bad json %q: %v", line, err)
		}
		return m
	}
	return send, recv
}

// TestServeHandshakeAndTools walks the MCP session an agent runs:
// initialize (older client version echoed back), the initialized
// notification (no reply), tools/list, a tools/call with a JSON result, a
// failing call as isError (not a protocol error), and an unknown method.
func TestServeHandshakeAndTools(t *testing.T) {
	send, recv := session(t, fakeHandler{})
	send(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"t","version":"0"}}}`)
	r := recv()
	res := r["result"].(map[string]any)
	if res["protocolVersion"] != "2024-11-05" {
		t.Fatalf("protocolVersion = %v", res["protocolVersion"])
	}
	if res["serverInfo"].(map[string]any)["name"] != "cove" {
		t.Fatalf("serverInfo = %v", res["serverInfo"])
	}
	send(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	send(`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	r = recv()
	if r["id"].(float64) != 2 {
		t.Fatalf("notification got a reply, or ids out of order: %v", r)
	}
	tools := r["result"].(map[string]any)["tools"].([]any)
	if len(tools) != 1 || tools[0].(map[string]any)["name"] != "echo" {
		t.Fatalf("tools = %v", tools)
	}
	send(`{"jsonrpc":"2.0","id":"s3","method":"tools/call","params":{"name":"echo","arguments":{"Text":"hi"}}}`)
	r = recv()
	if r["id"] != "s3" {
		t.Fatalf("string id not echoed: %v", r["id"])
	}
	content := r["result"].(map[string]any)["content"].([]any)[0].(map[string]any)
	if !strings.Contains(content["text"].(string), `"got": "hi"`) {
		t.Fatalf("content = %v", content)
	}
	send(`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"nope"}}`)
	r = recv()
	res = r["result"].(map[string]any)
	if res["isError"] != true || !strings.Contains(res["content"].([]any)[0].(map[string]any)["text"].(string), "no such tool") {
		t.Fatalf("tool failure not an isError result: %v", r)
	}
	send(`{"jsonrpc":"2.0","id":5,"method":"resources/list"}`)
	r = recv()
	if r["error"].(map[string]any)["code"].(float64) != -32601 {
		t.Fatalf("unknown method: %v", r)
	}
}

// TestServeConcurrent: a slow tool call must not block a ping behind it.
func TestServeConcurrent(t *testing.T) {
	send, recv := session(t, fakeHandler{})
	send(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"slow"}}`)
	send(`{"jsonrpc":"2.0","id":2,"method":"ping"}`)
	if r := recv(); r["id"].(float64) != 2 {
		t.Fatalf("ping waited behind the slow call: %v", r)
	}
	if r := recv(); r["id"].(float64) != 1 {
		t.Fatalf("slow result: %v", r)
	}
}

// TestListenAndProxy: the full path an agent takes — `cove mcp` proxying
// stdio to the editor's socket — round-trips a request, and Close removes
// the socket file.
func TestListenAndProxy(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "cove-1.sock")
	s, err := Listen(sock, fakeHandler{}, "test")
	if err != nil {
		t.Fatal(err)
	}
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	go func() { Proxy(sock, inR, outW); outW.Close() }()
	inW.Write([]byte(`{"jsonrpc":"2.0","id":7,"method":"tools/list"}` + "\n"))
	line, err := bufio.NewReader(outR).ReadString('\n')
	if err != nil || !strings.Contains(line, `"echo"`) {
		t.Fatalf("proxied reply = %q, %v", line, err)
	}
	inW.Close()
	s.Close()
	if _, err := os.Stat(sock); !os.IsNotExist(err) {
		t.Fatal("socket file not removed on Close")
	}
}

// TestMainWithoutCove: outside the editor `cove mcp` still speaks MCP with
// no tools, so a user-scope registration never breaks a plain session.
func TestMainWithoutCove(t *testing.T) {
	t.Setenv(EnvVar, filepath.Join(t.TempDir(), "gone.sock"))
	in := strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}` + "\n")
	var out strings.Builder
	if err := Main(in, &out, "test"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"tools":[]`) {
		t.Fatalf("out = %q", out.String())
	}
}

// TestSweepStale: a socket left by a dead pid is removed at Listen; a
// live sibling (this process) is kept.
func TestSweepStale(t *testing.T) {
	dir := t.TempDir()
	dead := filepath.Join(dir, "cove-999999999.sock")
	os.WriteFile(dead, nil, 0o600)
	mine := SocketPath(os.Getpid())
	if !strings.HasSuffix(mine, "cove-"+strconv.Itoa(os.Getpid())+".sock") {
		t.Fatalf("SocketPath = %q", mine)
	}
	live := filepath.Join(dir, filepath.Base(mine))
	os.WriteFile(live, nil, 0o600)
	s, err := Listen(filepath.Join(dir, "cove-1.sock"), fakeHandler{}, "test")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := os.Stat(dead); !os.IsNotExist(err) {
		t.Fatal("dead pid socket not swept")
	}
	if _, err := os.Stat(live); err != nil {
		t.Fatal("live pid socket swept")
	}
}
