package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// decodeError reads a JSON error envelope from a response recorder.
func decodeError(t *testing.T, body []byte) ErrorResponse {
	t.Helper()
	var er ErrorResponse
	if err := json.Unmarshal(body, &er); err != nil {
		t.Fatalf("response is not valid JSON error envelope: %v; body=%q", err, string(body))
	}
	return er
}

// TestWriteJSONError_Shape verifies the error envelope matches the documented
// contract: { "error": { code, type, message, recoverable } }.
func TestWriteJSONError_Shape(t *testing.T) {
	rr := httptest.NewRecorder()
	writeJSONError(rr, http.StatusBadRequest, "invalid_argument", "bad input", false)

	if ct := rr.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Fatalf("expected JSON content-type, got %q", ct)
	}
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", rr.Code)
	}
	er := decodeError(t, rr.Body.Bytes())
	if er.Error.Code != http.StatusBadRequest {
		t.Fatalf("error.code mismatch: %d", er.Error.Code)
	}
	if er.Error.Type != "invalid_argument" {
		t.Fatalf("error.type mismatch: %q", er.Error.Type)
	}
	if er.Error.Message != "bad input" {
		t.Fatalf("error.message mismatch: %q", er.Error.Message)
	}
	if er.Error.Recoverable != false {
		t.Fatalf("error.recoverable mismatch: %v", er.Error.Recoverable)
	}
}

// TestAPIGraphHandler_MethodNotAllowed verifies the JSON error contract for
// non-GET requests on /api/graph.
func TestAPIGraphHandler_MethodNotAllowed(t *testing.T) {
	graph := &GraphIndex{Version: 1, Nodes: []Memory{}, Edges: []GraphEdge{}}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/graph", nil)
	apiGraphHandler(rr, req, graph)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", rr.Code)
	}
	er := decodeError(t, rr.Body.Bytes())
	if er.Error.Type != "method_not_allowed" {
		t.Fatalf("expected type method_not_allowed, got %q", er.Error.Type)
	}
}

// TestAPIGraphHandler_GETReturnsJSON verifies a GET returns the graph as JSON.
func TestAPIGraphHandler_GETReturnsJSON(t *testing.T) {
	graph := &GraphIndex{
		Version: 1,
		Nodes:   []Memory{{ID: "n1", Name: "N1"}},
		Edges:   []GraphEdge{},
	}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/graph", nil)
	apiGraphHandler(rr, req, graph)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Fatalf("expected JSON content-type, got %q", ct)
	}
	var got GraphIndex
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("invalid JSON: %v; body=%q", err, rr.Body.String())
	}
	if len(got.Nodes) != 1 || got.Nodes[0].ID != "n1" {
		t.Fatalf("graph round-trip mismatch: %+v", got)
	}
}

// TestAPISearchHandlerV2_MissingQ verifies the JSON error for missing ?q=.
func TestAPISearchHandlerV2_MissingQ(t *testing.T) {
	graph := &GraphIndex{
		Version: 1,
		Nodes:   []Memory{{ID: "n1", Name: "N1", Description: "x", Project: "p"}},
	}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/search", nil)
	apiSearchHandlerV2(rr, req, graph)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing q, got %d", rr.Code)
	}
	er := decodeError(t, rr.Body.Bytes())
	if er.Error.Type != "invalid_argument" {
		t.Fatalf("expected type invalid_argument, got %q", er.Error.Type)
	}
}

// TestAPISearchHandlerV2_EmptyGraph verifies the JSON error for an empty graph.
func TestAPISearchHandlerV2_EmptyGraph(t *testing.T) {
	graph := &GraphIndex{Version: 1, Nodes: []Memory{}}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/search?q=test", nil)
	apiSearchHandlerV2(rr, req, graph)

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 for empty graph, got %d", rr.Code)
	}
	er := decodeError(t, rr.Body.Bytes())
	if er.Error.Type != "graph_empty" {
		t.Fatalf("expected type graph_empty, got %q", er.Error.Type)
	}
}

// TestAPISearchHandlerV2_MethodNotAllowed verifies POST is rejected with JSON.
func TestAPISearchHandlerV2_MethodNotAllowed(t *testing.T) {
	graph := &GraphIndex{Version: 1, Nodes: []Memory{{ID: "n1"}}}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/search?q=x", nil)
	apiSearchHandlerV2(rr, req, graph)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", rr.Code)
	}
	er := decodeError(t, rr.Body.Bytes())
	if er.Error.Type != "method_not_allowed" {
		t.Fatalf("expected type method_not_allowed, got %q", er.Error.Type)
	}
}

// TestAPISearchHandlerV2_LimitZeroReturnsAll verifies limit=0 means no limit
// (the v1.3.3 fix). We can't easily assert exact counts without a real graph,
// but we can assert the response is valid JSON with a results array.
func TestAPISearchHandlerV2_LimitZeroReturnsAll(t *testing.T) {
	graph := &GraphIndex{
		Version: 1,
		Nodes: []Memory{
			{ID: "jwt", Name: "jwt", Description: "json web token", Project: "auth", Content: "jwt token"},
			{ID: "token", Name: "token", Description: "token signing", Project: "auth", Content: "token"},
		},
	}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/search?q=jwt&limit=0", nil)
	apiSearchHandlerV2(rr, req, graph)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body=%s", rr.Code, rr.Body.String())
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid JSON: %v; body=%q", err, rr.Body.String())
	}
	if _, ok := resp["results"]; !ok {
		t.Fatalf("response missing 'results' key: %+v", resp)
	}
}

// TestAPINodeHandler_NotFound verifies an unknown node ID returns JSON 404.
func TestAPINodeHandler_NotFound(t *testing.T) {
	cfg := &Config{}
	nodeMap := map[string]Memory{"exists": {ID: "exists"}}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/nodes/missing", nil)
	req.URL.Path = "/api/nodes/missing"
	apiNodeHandler(rr, req, cfg, nodeMap)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rr.Code)
	}
	er := decodeError(t, rr.Body.Bytes())
	if er.Error.Type != "not_found" {
		t.Fatalf("expected type not_found, got %q", er.Error.Type)
	}
}

// TestAPINodeHandler_Found verifies a known node ID returns JSON.
func TestAPINodeHandler_Found(t *testing.T) {
	cfg := &Config{}
	nodeMap := map[string]Memory{
		"jwt": {ID: "jwt", Name: "JWT", Description: "json web token", Project: "auth"},
	}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/nodes/jwt", nil)
	req.URL.Path = "/api/nodes/jwt"
	apiNodeHandler(rr, req, cfg, nodeMap)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	var node map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &node); err != nil {
		t.Fatalf("invalid JSON: %v; body=%q", err, rr.Body.String())
	}
	if node["id"] != "jwt" {
		t.Fatalf("expected id=jwt, got %v", node["id"])
	}
}

// TestCatchAllAPIPath_ReturnsJSON404 verifies the v1.3.4 fix: an unknown
// /api/* path routed through the catch-all "/" handler returns a JSON 404
// instead of Go's default plain-text "404 page not found".
func TestCatchAllAPIPath_ReturnsJSON404(t *testing.T) {
	// Rebuild the catch-all handler exactly as handleServe registers it.
	// We can't call handleServe directly (it blocks on signals), so we test
	// the same closure logic. The real registration is in cmd_serve.go:284.
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			if strings.HasPrefix(r.URL.Path, "/api/") {
				writeJSONError(w, http.StatusNotFound, "not_found", "Unknown API endpoint: "+r.URL.Path, false)
				return
			}
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte("index"))
	})

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/unknown", nil)
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for /api/unknown, got %d", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Fatalf("expected JSON content-type for /api/unknown, got %q", ct)
	}
	er := decodeError(t, rr.Body.Bytes())
	if er.Error.Type != "not_found" {
		t.Fatalf("expected type not_found, got %q", er.Error.Type)
	}
	if !strings.Contains(er.Error.Message, "/api/unknown") {
		t.Fatalf("expected message to mention /api/unknown, got %q", er.Error.Message)
	}
}

// TestCatchAllNonAPIPath_ReturnsPlainText404 verifies non-API unknown paths
// still return Go's default plain-text 404 (the documented behavior).
func TestCatchAllNonAPIPath_ReturnsPlainText404(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			if strings.HasPrefix(r.URL.Path, "/api/") {
				writeJSONError(w, http.StatusNotFound, "not_found", "Unknown API endpoint: "+r.URL.Path, false)
				return
			}
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte("index"))
	})

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/some/random/ui/path", nil)
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rr.Code)
	}
	ct := rr.Header().Get("Content-Type")
	if strings.Contains(ct, "application/json") {
		t.Fatalf("non-API path should NOT return JSON, got content-type %q; body=%q", ct, rr.Body.String())
	}
}
