package main

import "testing"

// TestFindCommand_ValueFlags pins the core of #21: every value-taking
// global flag must have its value skipped so the value is not mistaken
// for the command.
func TestFindCommand_ValueFlags(t *testing.T) {
	for flag := range valueFlags {
		cmd, _ := findCommand([]string{"memgraph", flag, "value", "config"})
		if cmd != "config" {
			t.Errorf("%s <value> config: command = %q, want %q", flag, cmd, "config")
		}
	}
}

func TestFindCommand(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"plain command", []string{"memgraph", "config"}, "config"},
		{"valueless flag before command", []string{"memgraph", "--json", "config"}, "config"},
		{"short valueless flag", []string{"memgraph", "-j", "config"}, "config"},
		{"value flag", []string{"memgraph", "--project", "aplomb", "config"}, "config"},
		{"value flag inline", []string{"memgraph", "--project=aplomb", "config"}, "config"},
		{"mixed flags", []string{"memgraph", "--json", "--project", "x", "recall"}, "recall"},
		{"flags after command stop scan", []string{"memgraph", "recall", "--project", "x"}, "recall"},
		{"double-dash value not consumed", []string{"memgraph", "--project", "--json", "config"}, "config"},
		{"single-dash value is consumed", []string{"memgraph", "--project", "-j", "config"}, "config"},
		{"flag missing value", []string{"memgraph", "--project"}, ""},
		{"help long", []string{"memgraph", "--help"}, "--help"},
		{"help short", []string{"memgraph", "-h"}, "-h"},
		{"version long", []string{"memgraph", "--version"}, "--version"},
		{"version short", []string{"memgraph", "-v"}, "-v"},
		{"help after valueless flag", []string{"memgraph", "--json", "--help"}, "--help"},
		{"version after value flag", []string{"memgraph", "--project", "x", "--version"}, "--version"},
		{"no args", []string{"memgraph"}, ""},
		{"only valueless flags", []string{"memgraph", "--json", "-y"}, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cmd, _ := findCommand(tc.args)
			if cmd != tc.want {
				t.Errorf("findCommand(%v) = %q, want %q", tc.args, cmd, tc.want)
			}
		})
	}
}

// TestFindCommand_Index pins the command position, which main() uses to
// normalize argv so handlers parsing os.Args[2:] still see flags that
// preceded the command.
func TestFindCommand_Index(t *testing.T) {
	tests := []struct {
		args    []string
		wantIdx int
	}{
		{[]string{"memgraph", "config"}, 1},
		{[]string{"memgraph", "--project", "x", "config"}, 3},
		{[]string{"memgraph", "--json", "--tags", "a,b", "list"}, 4},
		{[]string{"memgraph", "--json"}, -1},
	}
	for _, tc := range tests {
		_, idx := findCommand(tc.args)
		if idx != tc.wantIdx {
			t.Errorf("findCommand(%v) index = %d, want %d", tc.args, idx, tc.wantIdx)
		}
	}
}
