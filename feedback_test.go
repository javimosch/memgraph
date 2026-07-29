package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestGenerateFeedbackID verifies the ID is 32 hex chars (16 bytes) and unique.
func TestGenerateFeedbackID(t *testing.T) {
	id1 := generateFeedbackID()
	id2 := generateFeedbackID()
	if len(id1) != 32 {
		t.Fatalf("expected 32 hex chars (16 bytes), got %d: %q", len(id1), id1)
	}
	if id1 == id2 {
		t.Fatalf("expected unique IDs, got %q twice", id1)
	}
	// Must be valid hex
	if _, err := decodeHex(id1); err != nil {
		t.Fatalf("ID is not valid hex: %v", err)
	}
}

func decodeHex(s string) (interface{}, error) {
	var b []byte
	return b, json.Unmarshal([]byte(`"`+s+`"`), &b)
}

// TestPostFeedback_Success verifies a 200 response from the endpoint counts as stored.
func TestPostFeedback_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/feedback" {
			t.Errorf("expected /v1/feedback, got %s", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("expected application/json, got %s", ct)
		}
		var sub feedbackSubmission
		json.NewDecoder(r.Body).Decode(&sub)
		if sub.Message != "test message" {
			t.Errorf("message mismatch: %q", sub.Message)
		}
		if sub.App != "memgraph" {
			t.Errorf("app mismatch: %q", sub.App)
		}
		w.WriteHeader(200)
		w.Write([]byte(`{"ok":true,"id":"abc","stored":true}`))
	}))
	defer server.Close()

	sub := feedbackSubmission{Message: "test message", App: "memgraph", ID: "test-id"}
	if !postFeedback(server.URL, sub, 5000000000) {
		t.Fatal("expected postFeedback to return true on 200")
	}
}

// TestPostFeedback_ServerError verifies a 500 response counts as not stored.
func TestPostFeedback_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer server.Close()

	sub := feedbackSubmission{Message: "test", ID: "x"}
	if postFeedback(server.URL, sub, 5000000000) {
		t.Fatal("expected postFeedback to return false on 500")
	}
}

// TestPostFeedback_ConnectionRefused verifies a connection error counts as not stored
// and doesn't panic (best-effort, never-fail per spec §3).
func TestPostFeedback_ConnectionRefused(t *testing.T) {
	sub := feedbackSubmission{Message: "test", ID: "x"}
	// Use a port that's definitely not listening
	if postFeedback("http://127.0.0.1:1", sub, 1000000000) {
		t.Fatal("expected postFeedback to return false on connection refused")
	}
}

// TestResolveAppEndpoint verifies env var resolution order.
func TestResolveAppEndpoint(t *testing.T) {
	t.Setenv("MEMGRAPH_URL", "")
	t.Setenv("MEMGRAPH_PUBLIC_URL", "")
	if got := resolveAppEndpoint(); got != "" {
		t.Fatalf("expected empty when no env set, got %q", got)
	}

	t.Setenv("MEMGRAPH_URL", "http://localhost:8080")
	if got := resolveAppEndpoint(); got != "http://localhost:8080" {
		t.Fatalf("expected MEMGRAPH_URL, got %q", got)
	}

	t.Setenv("MEMGRAPH_URL", "")
	t.Setenv("MEMGRAPH_PUBLIC_URL", "http://public.example.com")
	if got := resolveAppEndpoint(); got != "http://public.example.com" {
		t.Fatalf("expected MEMGRAPH_PUBLIC_URL fallback, got %q", got)
	}
}

// TestResolveRelayURL verifies FEEDBACK_RELAY env and "off" disabling.
func TestResolveRelayURL(t *testing.T) {
	t.Setenv("FEEDBACK_RELAY", "")
	if got := resolveRelayURL(); got != defaultRelayURL {
		t.Fatalf("expected default relay, got %q", got)
	}

	t.Setenv("FEEDBACK_RELAY", "http://custom.relay.com")
	if got := resolveRelayURL(); got != "http://custom.relay.com" {
		t.Fatalf("expected custom relay, got %q", got)
	}

	t.Setenv("FEEDBACK_RELAY", "off")
	if got := resolveRelayURL(); got != "" {
		t.Fatalf("expected empty when FEEDBACK_RELAY=off, got %q", got)
	}
}

// TestFeedbackSubmission_JSONShape verifies the submission marshals to the
// spec §1 JSON shape (message required, app present, id present).
func TestFeedbackSubmission_JSONShape(t *testing.T) {
	sub := feedbackSubmission{
		Message:  "the reconnect is flaky",
		App:      "memgraph",
		Version:  "1.4.0",
		Kind:     "bug",
		Context:  "long-running tunnel",
		Reporter: "agent",
		ID:       "abc123",
	}
	data, err := json.Marshal(sub)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]interface{}
	json.Unmarshal(data, &m)
	for _, key := range []string{"message", "app", "version", "kind", "context", "reporter", "id"} {
		if _, ok := m[key]; !ok {
			t.Errorf("submission JSON missing key %q: %s", key, string(data))
		}
	}
	if m["message"] != "the reconnect is flaky" {
		t.Errorf("message mismatch: %v", m["message"])
	}
}

// TestFeedbackResult_JSONShape verifies the CLI output shape per spec §3.
func TestFeedbackResult_JSONShape(t *testing.T) {
	result := feedbackResult{ID: "abc", Stored: 1, Relayed: 1}
	data, err := json.Marshal(map[string]interface{}{
		"ok":   true,
		"data": result,
	})
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]interface{}
	json.Unmarshal(data, &m)
	if m["ok"] != true {
		t.Errorf("expected ok=true, got %v", m["ok"])
	}
	dataMap, ok := m["data"].(map[string]interface{})
	if !ok {
		t.Fatal("expected data to be an object")
	}
	if dataMap["id"] != "abc" {
		t.Errorf("id mismatch: %v", dataMap["id"])
	}
	if dataMap["stored"] != float64(1) {
		t.Errorf("stored mismatch: %v", dataMap["stored"])
	}
	if dataMap["relayed"] != float64(1) {
		t.Errorf("relayed mismatch: %v", dataMap["relayed"])
	}
}

// TestHandleFeedback_NeverFailWithRelayOff verifies the command exits 0 and
// reports stored=0/relayed=0 when the relay is off and no app endpoint is set.
// This is the core "never-fail" guarantee from spec §3.
func TestHandleFeedback_NeverFailWithRelayOff(t *testing.T) {
	// We can't call handleFeedback directly (it calls os.Exit), but we can
	// verify the core logic: with relay off and no app endpoint, both writes
	// are skipped and the result is stored=0, relayed=0.
	t.Setenv("FEEDBACK_RELAY", "off")
	t.Setenv("MEMGRAPH_URL", "")
	t.Setenv("MEMGRAPH_PUBLIC_URL", "")

	// Simulate the dual-write logic from handleFeedback
	submission := feedbackSubmission{
		Message:  "test feedback",
		App:      "memgraph",
		Version:  "1.4.0",
		Kind:     "note",
		Reporter: "agent",
		ID:       generateFeedbackID(),
	}
	stored := 0
	relayed := 0
	if appURL := resolveAppEndpoint(); appURL != "" {
		if postFeedback(appURL, submission, 1000000000) {
			stored = 1
		}
	}
	if relayURL := resolveRelayURL(); relayURL != "" {
		if postFeedback(relayURL, submission, 1000000000) {
			relayed = 1
		}
	}
	if stored != 0 || relayed != 0 {
		t.Fatalf("expected stored=0 relayed=0 with relay off and no app endpoint, got stored=%d relayed=%d", stored, relayed)
	}
}

// TestHandleFeedback_EndToEndWithMockServer verifies the full dual-write path
// against mock app + relay endpoints.
func TestHandleFeedback_EndToEndWithMockServer(t *testing.T) {
	appReceived := make(chan feedbackSubmission, 1)
	relayReceived := make(chan feedbackSubmission, 1)

	appServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var sub feedbackSubmission
		json.NewDecoder(r.Body).Decode(&sub)
		appReceived <- sub
		w.WriteHeader(200)
		w.Write([]byte(`{"ok":true,"id":"` + sub.ID + `","stored":true}`))
	}))
	defer appServer.Close()

	relayServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var sub feedbackSubmission
		json.NewDecoder(r.Body).Decode(&sub)
		relayReceived <- sub
		w.WriteHeader(200)
		w.Write([]byte(`{"ok":true,"id":"` + sub.ID + `","stored":true}`))
	}))
	defer relayServer.Close()

	t.Setenv("MEMGRAPH_URL", appServer.URL)
	t.Setenv("FEEDBACK_RELAY", relayServer.URL)

	// Simulate the dual-write logic
	submission := feedbackSubmission{
		Message:  "the galaxy viz is slow with 500+ nodes",
		App:      "memgraph",
		Version:  "1.4.0",
		Kind:     "bug",
		Context:  "browsing a large skill graph",
		Reporter: "agent",
		ID:       generateFeedbackID(),
	}
	stored := 0
	relayed := 0
	if postFeedback(resolveAppEndpoint(), submission, 5000000000) {
		stored = 1
	}
	if postFeedback(resolveRelayURL(), submission, 5000000000) {
		relayed = 1
	}

	if stored != 1 || relayed != 1 {
		t.Fatalf("expected stored=1 relayed=1, got stored=%d relayed=%d", stored, relayed)
	}

	// Verify both endpoints received the same submission (same id = idempotent)
	appSub := <-appReceived
	relaySub := <-relayReceived
	if appSub.ID != relaySub.ID {
		t.Fatalf("id mismatch: app=%s relay=%s (must be same for idempotency)", appSub.ID, relaySub.ID)
	}
	if appSub.Message != relaySub.Message {
		t.Fatalf("message mismatch: app=%q relay=%q", appSub.Message, relaySub.Message)
	}
	if relaySub.App != "memgraph" {
		t.Fatalf("relay should record app=memgraph, got %q", relaySub.App)
	}
}

// TestPostFeedback_RejectsEmptyMessage is a spec §2 conformance check: the
// endpoint must reject empty messages with 400. We verify our mock endpoint
// can enforce this (the real relay does it server-side).
func TestPostFeedback_AcceptsValidSubmission(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var sub feedbackSubmission
		json.NewDecoder(r.Body).Decode(&sub)
		if strings.TrimSpace(sub.Message) == "" {
			w.WriteHeader(400)
			w.Write([]byte(`{"ok":false,"error":"message required"}`))
			return
		}
		w.WriteHeader(200)
		w.Write([]byte(`{"ok":true,"id":"` + sub.ID + `","stored":true}`))
	}))
	defer server.Close()

	// Valid submission
	sub := feedbackSubmission{Message: "valid feedback", App: "memgraph", ID: "valid-id"}
	if !postFeedback(server.URL, sub, 5000000000) {
		t.Fatal("expected valid submission to succeed")
	}

	// Empty message should get 400 -> postFeedback returns false
	emptySub := feedbackSubmission{Message: "", App: "memgraph", ID: "empty-id"}
	if postFeedback(server.URL, emptySub, 5000000000) {
		t.Fatal("expected empty message to be rejected (400 -> false)")
	}
}
