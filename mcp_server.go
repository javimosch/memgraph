package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
)

// MCP protocol version we support (latest stable).
const mcpProtocolVersion = "2025-11-25"

// jsonrpcMessage is the base JSON-RPC 2.0 envelope.
type jsonrpcMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *jsonrpcError   `json:"error,omitempty"`
}

type jsonrpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// mcpContentBlock is a single content block in a tool call response.
type mcpContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

// mcpToolResult is the result of a tools/call request.
type mcpToolResult struct {
	Content []mcpContentBlock `json:"content"`
	IsError bool              `json:"isError,omitempty"`
}

// mcpToolDef is a tool definition for tools/list.
type mcpToolDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

// handleMCP starts the stdio MCP server. It reads newline-delimited JSON-RPC
// messages from stdin and writes responses to stdout. Logs go to stderr.
func handleMCP(cfg *Config) {
	// Never write to stdout except JSON-RPC messages.
	// Redirect log output to stderr.
	log.SetOutput(os.Stderr)
	log.SetFlags(0)

	scanner := bufio.NewScanner(os.Stdin)
	// Allow large messages (memories can be big).
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)

	writer := bufio.NewWriter(os.Stdout)
	defer writer.Flush()

	initialized := false

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var msg jsonrpcMessage
		if err := json.Unmarshal(line, &msg); err != nil {
			writeResponse(writer, jsonrpcMessage{
				JSONRPC: "2.0",
				ID:      nil,
				Error: &jsonrpcError{
					Code:    -32700,
					Message: "Parse error",
				},
			})
			writer.Flush()
			continue
		}

		// Notifications (no ID) — no response needed.
		if len(msg.ID) == 0 {
			if msg.Method == "notifications/initialized" {
				initialized = true
			}
			continue
		}

		resp := dispatchMCP(cfg, &msg, &initialized)
		resp.ID = msg.ID
		writeResponse(writer, resp)
		writer.Flush()
	}

	if err := scanner.Err(); err != nil && err != io.EOF {
		log.Printf("MCP scanner error: %v", err)
	}
}

// dispatchMCP routes a JSON-RPC request to the appropriate handler.
func dispatchMCP(cfg *Config, msg *jsonrpcMessage, initialized *bool) jsonrpcMessage {
	switch msg.Method {
	case "initialize":
		return handleMCPInitialize(msg)
	case "notifications/initialized":
		*initialized = true
		return jsonrpcMessage{}
	case "tools/list":
		return handleMCPToolsList(msg)
	case "tools/call":
		return handleMCPToolsCall(cfg, msg)
	case "ping":
		// Simple ping for keepalive.
		return jsonrpcMessage{JSONRPC: "2.0", Result: json.RawMessage(`{}`)}
	default:
		return jsonrpcMessage{
			JSONRPC: "2.0",
			Error: &jsonrpcError{
				Code:    -32601,
				Message: fmt.Sprintf("Method not found: %s", msg.Method),
			},
		}
	}
}

// handleMCPInitialize responds to the initialize handshake.
func handleMCPInitialize(msg *jsonrpcMessage) jsonrpcMessage {
	result, _ := json.Marshal(map[string]any{
		"protocolVersion": mcpProtocolVersion,
		"capabilities": map[string]any{
			"tools": map[string]any{
				"listChanged": false,
			},
		},
		"serverInfo": map[string]any{
			"name":    "memgraph",
			"version": Version,
		},
		"instructions": "Memgraph memory system. Use memgraph_projects to discover available projects, memgraph_recall to search memories (returns compact section index by default), memgraph_read to read a full memory or specific section, memgraph_save to store new memories, and memgraph_recommend to get skill recommendations for a task.",
	})
	return jsonrpcMessage{JSONRPC: "2.0", Result: result}
}

// writeResponse writes a JSON-RPC message as a single line to the writer.
func writeResponse(w *bufio.Writer, msg jsonrpcMessage) {
	// Don't write responses for notifications (no ID and no error).
	if len(msg.ID) == 0 && msg.Error == nil && len(msg.Result) == 0 {
		return
	}
	data, err := json.Marshal(msg)
	if err != nil {
		// Last resort — write a generic error.
		fmt.Fprintf(w, `{"jsonrpc":"2.0","error":{"code":-32603,"message":"Internal error"}}`+"\n")
		return
	}
	w.Write(data)
	w.WriteByte('\n')
}
