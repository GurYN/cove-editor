package lsp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"testing"
	"time"
)

func newBufReader(r io.Reader) *bufio.Reader { return bufio.NewReader(r) }

// pipes wires a fake client/server pair over io.Pipe.
type pipes struct {
	clientIn  *io.PipeReader // server reads client output here
	clientOut *io.PipeWriter // conn writes here
	serverIn  *io.PipeReader // conn reads server output here
	serverOut *io.PipeWriter // server writes here
}

func newPipes() pipes {
	c2sR, c2sW := io.Pipe()
	s2cR, s2cW := io.Pipe()
	return pipes{clientIn: c2sR, clientOut: c2sW, serverIn: s2cR, serverOut: s2cW}
}

// fakeServer reads frames from in and answers via the provided handler.
func fakeServer(t *testing.T, in io.Reader, out io.Writer, handle func(req request) any) {
	t.Helper()
	go func() {
		br := newBufReader(in)
		for {
			msg, err := readFrame(br)
			if err != nil {
				return
			}
			var req request
			if json.Unmarshal(msg, &req) != nil {
				continue
			}
			if req.ID == nil {
				continue // notification
			}
			result := handle(req)
			data, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": *req.ID, "result": result})
			fmt.Fprintf(out, "Content-Length: %d\r\n\r\n%s", len(data), data)
		}
	}()
}

func TestCallRoundTrip(t *testing.T) {
	p := newPipes()
	fakeServer(t, p.clientIn, p.serverOut, func(req request) any {
		if req.Method != "test/echo" {
			t.Errorf("method = %q", req.Method)
		}
		return map[string]string{"echo": "yes"}
	})
	conn := newConn(p.clientOut, p.serverIn, nil, nil)
	var res struct {
		Echo string `json:"echo"`
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := conn.Call(ctx, "test/echo", map[string]int{"x": 1}, &res); err != nil {
		t.Fatal(err)
	}
	if res.Echo != "yes" {
		t.Fatalf("echo = %q", res.Echo)
	}
}

func TestCallCancellation(t *testing.T) {
	p := newPipes()
	cancelled := make(chan int64, 1)
	go func() { // server that never answers, but records $/cancelRequest
		br := newBufReader(p.clientIn)
		for {
			msg, err := readFrame(br)
			if err != nil {
				return
			}
			var req request
			json.Unmarshal(msg, &req)
			if req.Method == "$/cancelRequest" {
				b, _ := json.Marshal(req.Params)
				var p struct {
					ID int64 `json:"id"`
				}
				json.Unmarshal(b, &p)
				cancelled <- p.ID
			}
		}
	}()
	conn := newConn(p.clientOut, p.serverIn, nil, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	err := conn.Call(ctx, "test/slow", nil, nil)
	if err != context.DeadlineExceeded {
		t.Fatalf("err = %v", err)
	}
	select {
	case id := <-cancelled:
		if id != 1 {
			t.Fatalf("cancelled id = %d", id)
		}
	case <-time.After(time.Second):
		t.Fatal("no $/cancelRequest sent")
	}
}

func TestNotificationDispatchAndServerDeath(t *testing.T) {
	p := newPipes()
	go io.Copy(io.Discard, p.clientIn) // drain client writes
	got := make(chan string, 1)
	died := make(chan struct{})
	conn := newConn(p.clientOut, p.serverIn,
		func(method string, params json.RawMessage) { got <- method },
		func(error) { close(died) })

	data, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": "textDocument/publishDiagnostics", "params": map[string]any{}})
	fmt.Fprintf(p.serverOut, "Content-Length: %d\r\n\r\n%s", len(data), data)
	select {
	case m := <-got:
		if m != "textDocument/publishDiagnostics" {
			t.Fatalf("method = %q", m)
		}
	case <-time.After(time.Second):
		t.Fatal("notification not dispatched")
	}

	// Server dies: pending calls fail, onErr fires.
	p.serverOut.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := conn.Call(ctx, "test/x", nil, nil); err == nil {
		t.Fatal("call after death succeeded")
	}
	select {
	case <-died:
	case <-time.After(time.Second):
		t.Fatal("onErr not called")
	}
}

func TestUTF16Conversion(t *testing.T) {
	line := []byte("héllo 🌍 x") // é=2 bytes/1 unit, 🌍=4 bytes/2 units
	cases := []struct{ byteCol, utf16Col int }{
		{0, 0}, {1, 1}, {3, 2}, {7, 6}, {11, 8}, {13, 10},
	}
	for _, c := range cases {
		if got := UTF16Col(line, c.byteCol); got != c.utf16Col {
			t.Errorf("UTF16Col(%d) = %d, want %d", c.byteCol, got, c.utf16Col)
		}
		if got := ByteCol(line, c.utf16Col); got != c.byteCol {
			t.Errorf("ByteCol(%d) = %d, want %d", c.utf16Col, got, c.byteCol)
		}
	}
}
