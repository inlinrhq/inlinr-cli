// Package mcp is the Model Context Protocol server behind `inlinr mcp`: an
// AI assistant (Claude Code, Cursor, Claude Desktop…) starts it over stdio
// and asks Inlinr about the user's time and dependencies through it.
//
// The protocol is JSON-RPC 2.0, one message per line on stdin and stdout.
// Only what a tools-only server needs is implemented: initialize, ping,
// tools/list and tools/call. Stdout carries protocol messages and nothing
// else; anything for a person goes to stderr.
package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"
)

// LatestProtocol is answered when the client asks for a version we do not
// know; the client then decides whether it can talk to us.
const LatestProtocol = "2025-06-18"

var knownProtocols = map[string]bool{
	"2024-11-05": true,
	"2025-03-26": true,
	"2025-06-18": true,
}

const (
	codeParse          = -32700
	codeInvalidRequest = -32600
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
)

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// Server answers one client. Tools are called one at a time, in the order
// they arrive: an assistant waits for each answer anyway.
type Server struct {
	Name    string
	Version string
	Tools   *Tools

	mu  sync.Mutex
	out *json.Encoder
}

// Serve reads requests from in until it is closed and writes the answers to
// out. It returns nil at end of input.
func (s *Server) Serve(ctx context.Context, in io.Reader, out io.Writer) error {
	s.out = json.NewEncoder(out)
	scanner := bufio.NewScanner(in)
	// A pasted package.json can be long; 8 MiB is far past any real one.
	scanner.Buffer(make([]byte, 0, 64*1024), 8<<20)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		s.handle(ctx, line)
	}
	return scanner.Err()
}

func (s *Server) handle(ctx context.Context, line []byte) {
	var req request
	if err := json.Unmarshal(line, &req); err != nil {
		s.send(response{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcError{codeParse, "parse error"}})
		return
	}
	// A notification (no id) never gets an answer, known or not.
	if len(req.ID) == 0 {
		return
	}
	if req.JSONRPC != "2.0" || req.Method == "" {
		s.send(response{JSONRPC: "2.0", ID: req.ID, Error: &rpcError{codeInvalidRequest, "invalid request"}})
		return
	}
	result, rerr := s.dispatch(ctx, req)
	if rerr != nil {
		s.send(response{JSONRPC: "2.0", ID: req.ID, Error: rerr})
		return
	}
	s.send(response{JSONRPC: "2.0", ID: req.ID, Result: result})
}

func (s *Server) dispatch(ctx context.Context, req request) (any, *rpcError) {
	switch req.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(req.Params, &p)
		version := LatestProtocol
		if knownProtocols[p.ProtocolVersion] {
			version = p.ProtocolVersion
		}
		return map[string]any{
			"protocolVersion": version,
			"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
			"serverInfo":      map[string]any{"name": s.Name, "version": s.Version},
			"instructions":    instructions,
		}, nil
	case "ping":
		return map[string]any{}, nil
	case "tools/list":
		return map[string]any{"tools": s.Tools.List()}, nil
	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil || p.Name == "" {
			return nil, &rpcError{codeInvalidParams, "tools/call needs a tool name"}
		}
		res, ok := s.Tools.Call(ctx, p.Name, p.Arguments)
		if !ok {
			return nil, &rpcError{codeInvalidParams, fmt.Sprintf("unknown tool: %s", p.Name)}
		}
		return res, nil
	default:
		return nil, &rpcError{codeMethodNotFound, "method not found: " + req.Method}
	}
}

func (s *Server) send(r response) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Encode writes one line per value: the framing stdio expects.
	_ = s.out.Encode(r)
}

const instructions = `Inlinr tracks the user's coding time, the time and cost of their AI assistants, and the npm dependencies of the projects they follow.
Use time_summary for "how long did I spend on X" and for AI cost, log_time to record a meeting or call, check_dependencies to see what in a package.json is behind, deprecated or affected by an advisory, and package_releases / upgrade_notes before an upgrade.`
