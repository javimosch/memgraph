package main

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

// TestMCP_Initialize tests the MCP initialize handshake.
func TestMCP_Initialize(t *testing.T) {
	resp := sendMCPMessage(t, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"test","version":"1.0"}}}`)

	if resp.JSONRPC != "2.0" {
		t.Errorf("expected jsonrpc 2.0, got %s", resp.JSONRPC)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %s", resp.Error.Message)
	}

	var result map[string]any
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("failed to parse result: %v", err)
	}

	if result["protocolVersion"] != "2025-11-25" {
		t.Errorf("expected protocolVersion 2025-11-25, got %v", result["protocolVersion"])
	}

	serverInfo, ok := result["serverInfo"].(map[string]any)
	if !ok {
		t.Fatal("missing serverInfo")
	}
	if serverInfo["name"] != "memgraph" {
		t.Errorf("expected server name memgraph, got %v", serverInfo["name"])
	}

	caps, ok := result["capabilities"].(map[string]any)
	if !ok {
		t.Fatal("missing capabilities")
	}
	if _, ok := caps["tools"]; !ok {
		t.Error("expected tools capability")
	}
}

// TestMCP_ToolsList tests that tools/list returns all 5 tools.
func TestMCP_ToolsList(t *testing.T) {
	resp := sendMCPMessages(t, []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"test","version":"1.0"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized","params":{}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`,
	})

	// Get the last response (tools/list) — notifications are filtered out
	// so we expect 2 responses: initialize + tools/list
	if len(resp) < 2 {
		t.Fatalf("expected at least 2 responses, got %d", len(resp))
	}

	toolsListResp := resp[len(resp)-1]
	if toolsListResp.Error != nil {
		t.Fatalf("unexpected error: %s", toolsListResp.Error.Message)
	}

	var result struct {
		Tools []struct {
			Name        string          `json:"name"`
			Description string          `json:"description"`
			InputSchema json.RawMessage `json:"inputSchema"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(toolsListResp.Result, &result); err != nil {
		t.Fatalf("failed to parse tools list: %v", err)
	}

	if len(result.Tools) != 23 {
		t.Fatalf("expected 23 tools, got %d", len(result.Tools))
	}

	expectedTools := map[string]bool{
		"memgraph_recall":          false,
		"memgraph_read":            false,
		"memgraph_save":            false,
		"memgraph_list":            false,
		"memgraph_edit":            false,
		"memgraph_delete":          false,
		"memgraph_sessions":        false,
		"memgraph_import":          false,
		"memgraph_init":            false,
		"memgraph_projects":        false,
		"memgraph_profile":         false,
		"memgraph_status":          false,
		"memgraph_config":          false,
		"memgraph_attach":          false,
		"memgraph_demo":            false,
		"memgraph_bridge":          false,
		"memgraph_setup":           false,
		"memgraph_feedback":        false,
		"memgraph_recommend":       false,
		"memgraph_query":           false,
		"memgraph_related":         false,
		"memgraph_plans":           false,
		"memgraph_graph_from_dir":  false,
	}
	for _, tool := range result.Tools {
		if _, ok := expectedTools[tool.Name]; ok {
			expectedTools[tool.Name] = true
		} else {
			t.Errorf("unexpected tool: %s", tool.Name)
		}
		if tool.Description == "" {
			t.Errorf("tool %s has empty description", tool.Name)
		}
		if len(tool.InputSchema) == 0 {
			t.Errorf("tool %s has empty inputSchema", tool.Name)
		}
	}
	for name, found := range expectedTools {
		if !found {
			t.Errorf("missing tool: %s", name)
		}
	}
}

// TestMCP_ToolsCall_UnknownTool tests error handling for unknown tools.
func TestMCP_ToolsCall_UnknownTool(t *testing.T) {
	resp := sendMCPMessages(t, []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"test","version":"1.0"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized","params":{}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"nonexistent_tool","arguments":{}}}`,
	})

	lastResp := resp[len(resp)-1]
	if lastResp.Error == nil {
		t.Fatal("expected error for unknown tool")
	}
	if lastResp.Error.Code != -32601 {
		t.Errorf("expected error code -32601, got %d", lastResp.Error.Code)
	}
}

// TestMCP_ToolsCall_MemgraphProjects tests the memgraph_projects tool.
func TestMCP_ToolsCall_MemgraphProjects(t *testing.T) {
	resp := sendMCPMessages(t, []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"test","version":"1.0"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized","params":{}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"memgraph_projects","arguments":{}}}`,
	})

	lastResp := resp[len(resp)-1]
	if lastResp.Error != nil {
		t.Fatalf("unexpected error: %s", lastResp.Error.Message)
	}

	var result mcpToolResult
	if err := json.Unmarshal(lastResp.Result, &result); err != nil {
		t.Fatalf("failed to parse tool result: %v", err)
	}

	if result.IsError {
		t.Fatal("expected success, got isError")
	}
	if len(result.Content) == 0 {
		t.Fatal("expected content blocks")
	}
	if result.Content[0].Type != "text" {
		t.Errorf("expected text content, got %s", result.Content[0].Type)
	}
}

// TestMCP_ToolsCall_MemgraphStatus tests the memgraph_status tool.
func TestMCP_ToolsCall_MemgraphStatus(t *testing.T) {
	resp := sendMCPMessages(t, []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"test","version":"1.0"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized","params":{}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"memgraph_status","arguments":{"project":"memgraph"}}}`,
	})

	lastResp := resp[len(resp)-1]
	if lastResp.Error != nil {
		t.Fatalf("unexpected error: %s", lastResp.Error.Message)
	}

	var result mcpToolResult
	if err := json.Unmarshal(lastResp.Result, &result); err != nil {
		t.Fatalf("failed to parse tool result: %v", err)
	}
	if result.IsError {
		t.Fatal("expected success, got isError")
	}
	// Should return JSON with status field
	if len(result.Content) == 0 || result.Content[0].Text == "" {
		t.Fatal("expected non-empty text content")
	}
}

// TestMCP_ToolsCall_MemgraphList tests the memgraph_list tool.
func TestMCP_ToolsCall_MemgraphList(t *testing.T) {
	resp := sendMCPMessages(t, []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"test","version":"1.0"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized","params":{}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"memgraph_list","arguments":{"project":"memgraph","limit":3}}}`,
	})

	lastResp := resp[len(resp)-1]
	if lastResp.Error != nil {
		t.Fatalf("unexpected error: %s", lastResp.Error.Message)
	}

	var result mcpToolResult
	if err := json.Unmarshal(lastResp.Result, &result); err != nil {
		t.Fatalf("failed to parse tool result: %v", err)
	}
	if result.IsError {
		t.Fatal("expected success, got isError")
	}
}

// TestMCP_Ping tests the ping method.
func TestMCP_Ping(t *testing.T) {
	resp := sendMCPMessage(t, `{"jsonrpc":"2.0","id":1,"method":"ping","params":{}}`)

	if resp.Error != nil {
		t.Fatalf("unexpected error: %s", resp.Error.Message)
	}
	if len(resp.Result) == 0 {
		t.Error("expected non-empty result for ping")
	}
}

// TestMCP_UnknownMethod tests error handling for unknown methods.
func TestMCP_UnknownMethod(t *testing.T) {
	resp := sendMCPMessage(t, `{"jsonrpc":"2.0","id":1,"method":"nonexistent_method","params":{}}`)

	if resp.Error == nil {
		t.Fatal("expected error for unknown method")
	}
	if resp.Error.Code != -32601 {
		t.Errorf("expected error code -32601, got %d", resp.Error.Code)
	}
}

// TestMCP_ParseError tests handling of invalid JSON.
func TestMCP_ParseError(t *testing.T) {
	resp := sendMCPMessage(t, `{invalid json}`)

	if resp.Error == nil {
		t.Fatal("expected error for invalid JSON")
	}
	if resp.Error.Code != -32700 {
		t.Errorf("expected error code -32700, got %d", resp.Error.Code)
	}
}

// --- Helpers ---

// sendMCPMessage sends a single message to the MCP server and returns the response.
func sendMCPMessage(t *testing.T, msg string) jsonrpcMessage {
	responses := sendMCPMessages(t, []string{msg})
	if len(responses) == 0 {
		t.Fatal("no response received")
	}
	return responses[0]
}

// sendMCPMessages sends multiple messages to the MCP server and returns all responses.
// It builds the binary, spawns it as a subprocess, sends the messages via stdin,
// and reads responses from stdout.
func sendMCPMessages(t *testing.T, messages []string) []jsonrpcMessage {
	// Build the binary (use the test binary to avoid rebuild issues)
	cmd := exec.Command("go", "run", ".", "mcp")
	cmd.Dir = "."

	// Join messages with newlines
	input := strings.Join(messages, "\n") + "\n"
	cmd.Stdin = bytes.NewBufferString(input)

	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = nil // discard stderr

	if err := cmd.Run(); err != nil {
		// go run may return non-zero on stdin close, that's ok
		// as long as we got output
		if stdout.Len() == 0 {
			t.Fatalf("failed to run memgraph mcp: %v", err)
		}
	}

	// Parse newline-delimited JSON responses
	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	var responses []jsonrpcMessage
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var resp jsonrpcMessage
		if err := json.Unmarshal([]byte(line), &resp); err != nil {
			// Skip non-JSON lines (shouldn't happen in MCP)
			continue
		}
		// Skip notifications (no ID)
		if len(resp.ID) == 0 && resp.Error == nil {
			continue
		}
		responses = append(responses, resp)
	}

	return responses
}
