// Package bridge exposes a running Cove to the coding agent in its terminal
// panel over MCP (Model Context Protocol). The agent already has a shell;
// what it lacks is what the editor knows: warm language servers, the
// diagnostics they published, which files the user has open. Cove listens
// on a per-process Unix socket and `cove mcp` (a stdio MCP server, the
// transport agents speak) proxies to it — so the one-time setup is a
// single `claude mcp add` and every agent session started inside Cove
// finds its editor through the COVE_SOCKET env var.
//
// Wire format on both legs is MCP's stdio framing: one JSON-RPC 2.0
// message per line. The proxy is a byte pipe; the protocol lives here.
package bridge

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
)

// EnvVar carries the socket path into panel terminals.
const EnvVar = "COVE_SOCKET"

// protocolVersion is the newest MCP revision this server implements; a
// client asking for an older one gets its own version echoed back, per spec.
const protocolVersion = "2025-06-18"

// Tool is one MCP tool definition as tools/list reports it.
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

// Handler answers tool calls. Call returns the result to be JSON-encoded
// as the tool's text content; an error becomes an isError tool result (the
// agent sees the message and can retry), never a protocol failure.
type Handler interface {
	Tools() []Tool
	Call(ctx context.Context, name string, args json.RawMessage) (any, error)
}

// ---- JSON-RPC types ----

type message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Serve runs one MCP session: reads requests from r until EOF, writes
// responses to w. Requests are handled concurrently so a slow tool call
// (waiting on a language server) never blocks a ping.
func Serve(ctx context.Context, r io.Reader, w io.Writer, h Handler, version string) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var wmu sync.Mutex
	send := func(v any) {
		data, err := json.Marshal(v)
		if err != nil {
			return
		}
		wmu.Lock()
		defer wmu.Unlock()
		w.Write(append(data, '\n'))
	}
	var wg sync.WaitGroup
	defer wg.Wait()
	br := bufio.NewReaderSize(r, 1<<20)
	for {
		line, err := br.ReadBytes('\n')
		if len(strings.TrimSpace(string(line))) > 0 {
			var msg message
			if json.Unmarshal(line, &msg) != nil || msg.Method == "" {
				if len(msg.ID) > 0 {
					send(message{JSONRPC: "2.0", ID: msg.ID, Error: &rpcError{Code: -32600, Message: "invalid request"}})
				}
			} else if len(msg.ID) == 0 || string(msg.ID) == "null" {
				// notification: initialized, cancelled, progress — nothing to do
			} else {
				wg.Add(1)
				go func(msg message) {
					defer wg.Done()
					send(handle(ctx, msg, h, version))
				}(msg)
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
	}
}

func handle(ctx context.Context, msg message, h Handler, version string) message {
	reply := message{JSONRPC: "2.0", ID: msg.ID}
	switch msg.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		json.Unmarshal(msg.Params, &p)
		pv := protocolVersion
		if p.ProtocolVersion != "" && p.ProtocolVersion < protocolVersion {
			pv = p.ProtocolVersion
		}
		reply.Result = map[string]any{
			"protocolVersion": pv,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "cove", "version": version},
			"instructions": "Cove is the editor hosting this session. Its tools answer from the " +
				"user's running language servers and open buffers: prefer them over grep or a " +
				"rebuild for diagnostics, definitions, references, symbols and renames.",
		}
	case "ping":
		reply.Result = map[string]any{}
	case "tools/list":
		tools := h.Tools()
		if tools == nil {
			tools = []Tool{}
		}
		reply.Result = map[string]any{"tools": tools}
	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(msg.Params, &p); err != nil || p.Name == "" {
			reply.Error = &rpcError{Code: -32602, Message: "tools/call: name required"}
			return reply
		}
		res, err := h.Call(ctx, p.Name, p.Arguments)
		if err != nil {
			reply.Result = toolResult(err.Error(), true)
			return reply
		}
		text, ok := res.(string)
		if !ok {
			data, _ := json.MarshalIndent(res, "", " ")
			text = string(data)
		}
		reply.Result = toolResult(text, false)
	default:
		reply.Error = &rpcError{Code: -32601, Message: "method not found: " + msg.Method}
	}
	return reply
}

func toolResult(text string, isErr bool) map[string]any {
	r := map[string]any{"content": []map[string]any{{"type": "text", "text": text}}}
	if isErr {
		r["isError"] = true
	}
	return r
}

// ---- socket server (inside Cove) ----

// Server accepts agent connections on a Unix socket, one MCP session each.
type Server struct {
	path string
	ln   net.Listener
	h    Handler
	ver  string
	wg   sync.WaitGroup
}

// Listen starts serving h on path, replacing a stale socket file and
// sweeping dead siblings in the same directory.
func Listen(path string, h Handler, version string) (*Server, error) {
	sweepStale(filepath.Dir(path))
	os.Remove(path)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	os.Chmod(path, 0o600) // the socket answers as the user; nobody else connects
	s := &Server{path: path, ln: ln, h: h, ver: version}
	s.wg.Add(1)
	go s.accept()
	return s, nil
}

func (s *Server) Path() string { return s.path }

func (s *Server) accept() {
	defer s.wg.Done()
	for {
		c, err := s.ln.Accept()
		if err != nil {
			return
		}
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			defer c.Close()
			Serve(context.Background(), c, c, s.h, s.ver)
		}()
	}
}

// Close stops accepting and removes the socket file. Sessions in flight
// finish their current request; their next read fails.
func (s *Server) Close() {
	if s == nil {
		return
	}
	s.ln.Close()
	os.Remove(s.path)
}

// SocketPath is the per-process socket location: XDG_RUNTIME_DIR when set
// (Linux sessions), else the user cache dir. Unix socket paths cap at ~104
// bytes on macOS, so the name stays short and the cache dir is never the
// workspace.
func SocketPath(pid int) string {
	dir := os.Getenv("XDG_RUNTIME_DIR")
	if dir == "" {
		if c, err := os.UserCacheDir(); err == nil {
			dir = filepath.Join(c, "cove")
		} else {
			dir = os.TempDir()
		}
	}
	return filepath.Join(dir, fmt.Sprintf("cove-%d.sock", pid))
}

// sweepStale removes cove-<pid>.sock files whose process is gone (a crash
// or SIGKILL skips Close).
func sweepStale(dir string) {
	matches, _ := filepath.Glob(filepath.Join(dir, "cove-*.sock"))
	for _, p := range matches {
		base := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(p), "cove-"), ".sock")
		pid, err := strconv.Atoi(base)
		if err != nil || pid == os.Getpid() {
			continue
		}
		if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
			os.Remove(p)
		}
	}
}

// ---- stdio side (`cove mcp`) ----

// Proxy pipes an agent's stdio MCP stream to the Cove socket at path.
// Stdin EOF half-closes the socket so replies still in flight drain to w;
// it returns once the editor side closes.
func Proxy(path string, r io.Reader, w io.Writer) error {
	c, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		return err
	}
	defer c.Close()
	go func() {
		io.Copy(c, r)
		c.CloseWrite()
	}()
	io.Copy(w, c)
	return nil
}

// Main is `cove mcp`: proxy to the editor named by COVE_SOCKET, or — run
// outside Cove, or after it quit — serve an empty tool list so an agent
// with Cove registered at user scope still starts cleanly.
func Main(r io.Reader, w io.Writer, version string) error {
	if p := os.Getenv(EnvVar); p != "" {
		if err := Proxy(p, r, w); err == nil {
			return nil
		}
		fmt.Fprintf(os.Stderr, "cove mcp: editor at %s unreachable; serving no tools\n", p)
	} else {
		fmt.Fprintln(os.Stderr, "cove mcp: not running inside Cove (COVE_SOCKET unset); serving no tools")
	}
	return Serve(context.Background(), r, w, noTools{}, version)
}

type noTools struct{}

func (noTools) Tools() []Tool { return nil }
func (noTools) Call(context.Context, string, json.RawMessage) (any, error) {
	return nil, errors.New("not running inside Cove")
}
