package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"
)

func TestGuideDataConformsToCliGuideShape(t *testing.T) {
	data := guideData()
	for _, key := range []string{"memgraph", "one_liner", "model", "loop", "concepts", "commands", "examples", "gotchas", "version", "see_also"} {
		if _, ok := data[key]; !ok {
			t.Fatalf("guide missing required field %q", key)
		}
	}
	if got := data["version"]; got != Version {
		t.Fatalf("guide version = %v, want %s", got, Version)
	}
	if loop, ok := data["loop"].([]string); !ok || len(loop) < 4 {
		t.Fatalf("guide loop should contain at least four ordered steps, got %#v", data["loop"])
	}
	if examples, ok := data["examples"].([]map[string]interface{}); !ok || len(examples) < 2 {
		t.Fatalf("guide should contain examples, got %#v", data["examples"])
	}
	if _, err := json.Marshal(data); err != nil {
		t.Fatalf("guide must marshal as JSON: %v", err)
	}
}

func TestHelpJSONCatalogListsGuideAndCommands(t *testing.T) {
	catalog := commandCatalog()
	if catalog["name"] != "memgraph" {
		t.Fatalf("catalog name = %v", catalog["name"])
	}
	commands, ok := catalog["commands"].([]map[string]interface{})
	if !ok {
		t.Fatalf("catalog commands have unexpected type: %T", catalog["commands"])
	}
	seen := map[string]bool{}
	for _, command := range commands {
		name, _ := command["name"].(string)
		seen[name] = true
		if command["usage"] == "" || command["summary"] == "" {
			t.Errorf("catalog command %q lacks usage or summary: %#v", name, command)
		}
	}
	for _, name := range []string{"guide", "help-json", "help", "remember", "recall", "recommend", "serve", "feedback", "mcp"} {
		if !seen[name] {
			t.Errorf("catalog missing command %q", name)
		}
	}
}

func TestGuideHumanRendering(t *testing.T) {
	human := guideHuman()
	for _, want := range []string{"# memgraph", "Canonical loop", "memgraph help-json", "--json"} {
		if !strings.Contains(human, want) {
			t.Errorf("human guide missing %q", want)
		}
	}
}

func TestGuideCommandTailIgnoresGlobalFlags(t *testing.T) {
	args := []string{"memgraph", "--json", "--memory-dir", "/tmp/store", "guide", "--human"}
	got := commandTail(args, "guide")
	if len(got) != 1 || got[0] != "--human" {
		t.Fatalf("commandTail = %#v, want [--human]", got)
	}
}

func TestGuideRejectsUnknownArgument(t *testing.T) {
	if testing.Short() {
		t.Skip("subprocess test disabled in short mode")
	}
	cmd := exec.Command("go", "run", ".", "guide", "--bad")
	if err := cmd.Run(); err == nil {
		t.Fatal("guide --bad should exit nonzero")
	}
}

func TestGuideHTTPRoutes(t *testing.T) {
	mux := http.NewServeMux()
	registerGuideRoutes(mux)

	guideReq := httptest.NewRequest(http.MethodGet, "/guide", nil)
	guideRec := httptest.NewRecorder()
	mux.ServeHTTP(guideRec, guideReq)
	if guideRec.Code != http.StatusOK || !strings.Contains(guideRec.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("GET /guide: status=%d content-type=%q", guideRec.Code, guideRec.Header().Get("Content-Type"))
	}
	var body map[string]interface{}
	if err := json.Unmarshal(guideRec.Body.Bytes(), &body); err != nil {
		t.Fatalf("GET /guide returned invalid JSON: %v", err)
	}
	data, ok := body["data"].(map[string]interface{})
	if !ok || data["one_liner"] == nil {
		t.Fatal("GET /guide missing enveloped one_liner")
	}

	badReq := httptest.NewRequest(http.MethodPost, "/llms.txt", nil)
	badRec := httptest.NewRecorder()
	mux.ServeHTTP(badRec, badReq)
	if badRec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST /llms.txt: status=%d, want 405", badRec.Code)
	}

	llmsReq := httptest.NewRequest(http.MethodGet, "/llms.txt", nil)
	llmsRec := httptest.NewRecorder()
	mux.ServeHTTP(llmsRec, llmsReq)
	if llmsRec.Code != http.StatusOK || !strings.Contains(llmsRec.Header().Get("Content-Type"), "text/plain") {
		t.Fatalf("GET /llms.txt: status=%d content-type=%q", llmsRec.Code, llmsRec.Header().Get("Content-Type"))
	}
	if !strings.Contains(llmsRec.Body.String(), "memgraph guide") {
		t.Fatal("GET /llms.txt must point agents at memgraph guide")
	}
}
