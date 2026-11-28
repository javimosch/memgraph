package main

import "testing"

// The web view has no login, so only an explicit --host exposes it.
func TestServeAddress(t *testing.T) {
	for _, c := range []struct {
		args  []string
		addr  string
		local bool
	}{
		{nil, "127.0.0.1:8080", true},
		{[]string{"--port", "9000"}, "127.0.0.1:9000", true},
		{[]string{"--host", "0.0.0.0"}, "0.0.0.0:8080", false},
		{[]string{"--host=0.0.0.0", "--port", "9000"}, "0.0.0.0:9000", false},
		{[]string{"--host", "::1"}, "[::1]:8080", true},
		{[]string{"--host", "localhost"}, "localhost:8080", true},
		{[]string{"--host", "192.168.1.10"}, "192.168.1.10:8080", false},
	} {
		_, opts := parseCommandArgs(c.args)
		addr, local := serveAddress(opts)
		if addr != c.addr || local != c.local {
			t.Errorf("%v: got %s local=%v, want %s local=%v", c.args, addr, local, c.addr, c.local)
		}
	}
	// A flag's value must not be taken for the command.
	if cmd, _ := findCommand([]string{"memgraph", "--host", "0.0.0.0", "serve"}); cmd != "serve" {
		t.Errorf("command = %q", cmd)
	}
}
