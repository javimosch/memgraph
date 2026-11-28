package main

import (
	"os"
	"strings"
	"testing"
)

// A server command that is mistaken for a client one would read and create
// local config on the server, and break the push hook.
func TestServerCommandsAreRecognisedWithFlagsAnywhere(t *testing.T) {
	for line, want := range map[string]bool{
		"serve":                                       true,
		"hook pre-receive":                            true,
		"admin project list":                          true,
		"--json admin token list":                     true,
		"-j -y admin project list":                    true,
		"--data /d serve":                             true,
		"--data=/d --listen 127.0.0.1:1 serve":        true,
		"--name admin --project p admin token create": true,
		"sync":                       false,
		"--project serve sync":       false,
		"--json status":              false,
		"clone admin":                false,
		"--server https://x clone p": false,
		"local serve --project p":    false,
		"":                           false,
	} {
		if got := isCloudServerCommand(strings.Fields(line)); got != want {
			t.Errorf("isCloudServerCommand(%q) = %v, want %v", line, got, want)
		}
	}
}

// The order main() leaves arguments in when global flags precede "cloud".
func TestParseServerArgs(t *testing.T) {
	a, err := parseServerArgs(strings.Fields("--json --data /d admin token create --name vm --project a --project=b"))
	if err != nil || a.data != "/d" || a.name != "vm" || a.projects.String() != "a,b" || strings.Join(a.words, " ") != "admin token create" {
		t.Fatalf("%+v %v", a, err)
	}
	if _, err := parseServerArgs([]string{"serve", "--listn", "x"}); err == nil {
		t.Fatal("a mistyped option must not be ignored")
	}
	os.Unsetenv("MEMGRAPH_CLOUD_DATA")
}

func TestCheckListen(t *testing.T) {
	for addr, ok := range map[string]bool{
		"127.0.0.1:8787": true, "localhost:0": true, "[::1]:8787": true,
		"0.0.0.0:8787": false, ":8787": false, "192.168.1.10:8787": false, "example.invalid:80": false, "nonsense": false,
	} {
		if (checkListen(addr, false) == nil) != ok {
			t.Errorf("checkListen(%q) ok = %v, want %v", addr, !ok, ok)
		}
	}
	if checkListen("0.0.0.0:8787", true) != nil {
		t.Error("override ignored")
	}
}
