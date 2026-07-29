package main

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// feedbackSubmission is the JSON body for POST /v1/feedback per cli-feedback-spec §1.
type feedbackSubmission struct {
	Message  string `json:"message"`
	App      string `json:"app"`
	Version  string `json:"version,omitempty"`
	Kind     string `json:"kind,omitempty"`
	Context  string `json:"context,omitempty"`
	Reporter string `json:"reporter,omitempty"`
	ID       string `json:"id"`
}

// feedbackResult is the CLI output: which writes succeeded.
type feedbackResult struct {
	ID      string `json:"id"`
	Stored  int    `json:"stored"`
	Relayed int    `json:"relayed"`
}

// defaultRelayURL is the central relay per cli-feedback-spec §3.
const defaultRelayURL = "https://feedback.intrane.fr"

// generateFeedbackID generates 16 random bytes hex-encoded (spec §4 reference).
func generateFeedbackID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// Fallback: timestamp-based ID if crypto/rand fails (shouldn't happen)
		return fmt.Sprintf("%x", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

// postFeedback sends a submission to a feedback endpoint (spec §2).
// Returns true if the write succeeded (stored/relayed), false otherwise.
// Never panics — best-effort per spec §3.
func postFeedback(endpoint string, body feedbackSubmission, timeout time.Duration) bool {
	data, err := json.Marshal(body)
	if err != nil {
		return false
	}
	client := &http.Client{Timeout: timeout}
	req, err := http.NewRequest(http.MethodPost, endpoint+"/v1/feedback", bytes.NewReader(data))
	if err != nil {
		return false
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	return resp.StatusCode >= 200 && resp.StatusCode < 300
}

// resolveAppEndpoint determines the app's own feedback endpoint (spec §3).
// Order: MEMGRAPH_URL, then MEMGRAPH_PUBLIC_URL, then empty (relay-only).
func resolveAppEndpoint() string {
	for _, env := range []string{"MEMGRAPH_URL", "MEMGRAPH_PUBLIC_URL"} {
		if v := os.Getenv(env); v != "" {
			return v
		}
	}
	return ""
}

// resolveRelayURL determines the relay endpoint (spec §3).
// FEEDBACK_RELAY env, default https://feedback.intrane.fr. "off" disables.
func resolveRelayURL() string {
	v := os.Getenv("FEEDBACK_RELAY")
	if v == "off" {
		return ""
	}
	if v != "" {
		return v
	}
	return defaultRelayURL
}

// handleFeedback implements `memgraph feedback` per cli-feedback-spec §3.
//
//	memgraph feedback "<message>" [-kind bug|idea|praise|note] [-context "<what you were doing>"]
//
// Dual-writes (best-effort) to the app endpoint and the relay. Never fails the
// caller — exits 0 even when both writes fail. Reports which succeeded.
func handleFeedback(cfg *Config) {
	args := os.Args[2:]
	var messageParts []string
	kind := "note"
	context := ""

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-kind", "--kind":
			if i+1 < len(args) {
				kind = args[i+1]
				i++
			}
		case "-context", "--context":
			if i+1 < len(args) {
				context = args[i+1]
				i++
			}
		default:
			if !strings.HasPrefix(args[i], "-") {
				messageParts = append(messageParts, args[i])
			}
		}
	}

	message := strings.TrimSpace(strings.Join(messageParts, " "))
	if message == "" {
		if jsonOutput {
			fmt.Println(`{"ok":false,"error":{"code":85,"type":"invalid_argument","message":"Feedback message required. Usage: memgraph feedback \"<message>\" [-kind bug|idea|praise|note] [-context \"...\"]","recoverable":false}}`)
		} else {
			fmt.Fprintln(os.Stderr, `Usage: memgraph feedback "<message>" [-kind bug|idea|praise|note] [-context "<what you were doing>"]`)
		}
		os.Exit(85)
	}

	// Build submission per spec §1
	reporter := os.Getenv("USER")
	if reporter == "" {
		reporter = "agent"
	}
	submission := feedbackSubmission{
		Message:  message,
		App:      "memgraph",
		Version:  Version,
		Kind:     kind,
		Context:  context,
		Reporter: reporter,
		ID:       generateFeedbackID(),
	}

	// Dual-write, best-effort, never-fail (spec §3)
	timeout := 10 * time.Second
	stored := 0
	relayed := 0

	// Write 1: app's own endpoint
	if appURL := resolveAppEndpoint(); appURL != "" {
		if postFeedback(appURL, submission, timeout) {
			stored = 1
		} else {
			fmt.Fprintf(os.Stderr, "warning: app endpoint write failed (%s)\n", appURL)
		}
	}

	// Write 2: central relay
	if relayURL := resolveRelayURL(); relayURL != "" {
		if postFeedback(relayURL, submission, timeout) {
			relayed = 1
		} else {
			fmt.Fprintf(os.Stderr, "warning: relay write failed (%s)\n", relayURL)
		}
	}

	// Output: never-fail, exit 0 (spec §3)
	result := feedbackResult{ID: submission.ID, Stored: stored, Relayed: relayed}
	if jsonOutput {
		data, _ := json.Marshal(map[string]interface{}{
			"ok":   true,
			"data": result,
		})
		fmt.Println(string(data))
	} else {
		fmt.Printf("Feedback submitted (id: %s, stored: %d, relayed: %d)\n", result.ID, result.Stored, result.Relayed)
	}
}
