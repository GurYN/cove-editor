package lsp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
)

// conn is a JSON-RPC 2.0 connection with LSP Content-Length framing.
// Writes are mutex-serialized; the read loop dispatches responses to
// pending calls and notifications to the handler.
type conn struct {
	w       io.Writer
	wmu     sync.Mutex
	nextID  int64
	pending map[int64]chan response
	pmu     sync.Mutex
	notify  func(method string, params json.RawMessage)
	onErr   func(error) // read loop terminated (EOF = server died)
}

type request struct {
	JSONRPC string `json:"jsonrpc"`
	ID      *int64 `json:"id,omitempty"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type response struct {
	// Raw: the spec allows string ids and servers use them (TS7's native
	// LSP tags its own requests "ts1", "ts2", …). Parsed leniently, echoed
	// verbatim when replying.
	ID     json.RawMessage `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *respError      `json:"error"`
	Method string          `json:"method"` // set on server->client notifications
	Params json.RawMessage `json:"params"`
}

type respError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *respError) Error() string { return fmt.Sprintf("lsp: %s (%d)", e.Message, e.Code) }

func newConn(w io.Writer, r io.Reader, notify func(string, json.RawMessage), onErr func(error)) *conn {
	c := &conn{w: w, pending: map[int64]chan response{}, notify: notify, onErr: onErr}
	go c.readLoop(r)
	return c
}

func (c *conn) write(v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	c.wmu.Lock()
	defer c.wmu.Unlock()
	if _, err := fmt.Fprintf(c.w, "Content-Length: %d\r\n\r\n", len(data)); err != nil {
		return err
	}
	_, err = c.w.Write(data)
	return err
}

// Notify sends a notification (no response expected).
func (c *conn) Notify(method string, params any) error {
	return c.write(request{JSONRPC: "2.0", Method: method, Params: params})
}

// Call sends a request and decodes the response into result (which may be
// nil). Context cancellation sends $/cancelRequest and returns ctx.Err().
func (c *conn) Call(ctx context.Context, method string, params, result any) error {
	c.pmu.Lock()
	c.nextID++
	id := c.nextID
	ch := make(chan response, 1)
	c.pending[id] = ch
	c.pmu.Unlock()
	defer func() {
		c.pmu.Lock()
		delete(c.pending, id)
		c.pmu.Unlock()
	}()

	if err := c.write(request{JSONRPC: "2.0", ID: &id, Method: method, Params: params}); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		c.Notify("$/cancelRequest", map[string]int64{"id": id})
		return ctx.Err()
	case resp, ok := <-ch:
		if !ok {
			return io.ErrUnexpectedEOF
		}
		if resp.Error != nil {
			return resp.Error
		}
		if result != nil && len(resp.Result) > 0 {
			return json.Unmarshal(resp.Result, result)
		}
		return nil
	}
}

func (c *conn) readLoop(r io.Reader) {
	br := bufio.NewReaderSize(r, 1<<20)
	for {
		msg, err := readFrame(br)
		if err != nil {
			c.pmu.Lock()
			for _, ch := range c.pending {
				close(ch)
			}
			c.pending = map[int64]chan response{}
			c.pmu.Unlock()
			if c.onErr != nil {
				c.onErr(err)
			}
			return
		}
		var resp response
		if json.Unmarshal(msg, &resp) != nil {
			continue
		}
		hasID := len(resp.ID) > 0 && string(resp.ID) != "null"
		switch {
		case resp.Method != "" && !hasID: // notification
			if c.notify != nil {
				c.notify(resp.Method, resp.Params)
			}
		case resp.Method != "": // server->client request: surface, reply
			if c.notify != nil {
				c.notify(resp.Method, resp.Params)
			}
			var result any // null satisfies most server requests, but
			switch resp.Method {
			case "workspace/applyEdit": // applied=false reads as client failure
				result = map[string]bool{"applied": true}
			case "window/showDocument": // success=false likewise
				result = map[string]bool{"success": true}
			}
			c.write(map[string]any{"jsonrpc": "2.0", "id": resp.ID, "result": result})
		case hasID: // response to one of our calls (ids are always numeric)
			var id int64
			if json.Unmarshal(resp.ID, &id) != nil {
				continue
			}
			c.pmu.Lock()
			ch := c.pending[id]
			c.pmu.Unlock()
			if ch != nil {
				ch <- resp
			}
		}
	}
}

func readFrame(br *bufio.Reader) ([]byte, error) {
	length := -1
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		if v, ok := strings.CutPrefix(line, "Content-Length: "); ok {
			length, err = strconv.Atoi(strings.TrimSpace(v))
			if err != nil {
				return nil, err
			}
		}
	}
	if length < 0 {
		return nil, fmt.Errorf("lsp: frame without Content-Length")
	}
	buf := make([]byte, length)
	_, err := io.ReadFull(br, buf)
	return buf, err
}
