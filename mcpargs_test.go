package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"
)

// state sums up every file a test box holds.
func (b *box) state() string {
	var lines []string
	err := filepath.WalkDir(b.home, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if d.Name() != ".memgraph.lock" {
			lines = append(lines, fmt.Sprintf("%s %x", path, sha256.Sum256(data)))
		}
		return nil
	})
	if err != nil {
		b.t.Fatal(err)
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

type mcpCall struct {
	tool string
	args map[string]any
}

func admin(command string, args map[string]any) mcpCall {
	return mcpCall{"memgraph_admin", map[string]any{"command": command, "args": args}}
}

// refused runs calls that must be turned down without changing anything.
func refused(t *testing.T, calls map[string]func(id, skills string) mcpCall) {
	t.Helper()
	b := newBox(t)
	id := b.save("keep me")
	b.run("attach", "mine")
	skills := skillDir(t, "alpha")
	writeString(t, filepath.Join(b.repo, "AGENTS.md"), "mine\n")
	before := b.state()
	for name, call := range calls {
		c := call(id, skills)
		reply := b.mcp(c.tool, c.args)
		if !strings.Contains(reply, `"isError":true`) || !strings.Contains(reply, "nothing was done") {
			t.Errorf("%s: not refused: %s", name, reply)
		}
		if after := b.state(); after != before {
			t.Fatalf("%s changed files", name)
		}
	}
}

// An argument a tool does not know may be a safety option the caller relies on.
func TestMCPRefusesUnknownArguments(t *testing.T) {
	with := func(tool string, args map[string]any) func(string, string) mcpCall {
		return func(id, _ string) mcpCall {
			if args["id"] == "ID" {
				args["id"] = id
			}
			args["dry_run"] = true
			return mcpCall{tool, args}
		}
	}
	adm := func(command string, args map[string]any) func(string, string) mcpCall {
		return func(_, skills string) mcpCall {
			if args["sync_dir"] == "SKILLS" {
				args["sync_dir"] = skills
			}
			if args["sync_dirs"] == "SKILLS" {
				args["sync_dirs"] = []any{skills}
			}
			args["dry_run"] = true
			return admin(command, args)
		}
	}
	refused(t, map[string]func(string, string) mcpCall{
		"delete":         with("memgraph_delete", map[string]any{"id": "ID"}),
		"edit":           with("memgraph_edit", map[string]any{"id": "ID", "text": "x"}),
		"supersede":      with("memgraph_supersede", map[string]any{"id": "ID", "text": "x"}),
		"save":           with("memgraph_save", map[string]any{"text": "x"}),
		"projects":       with("memgraph_projects", map[string]any{"repair": true}),
		"recall":         with("memgraph_recall", map[string]any{"query": "keep"}),
		"read":           with("memgraph_read", map[string]any{"id": "ID"}),
		"list":           with("memgraph_list", map[string]any{}),
		"recommend":      with("memgraph_recommend", map[string]any{"task": "x"}),
		"verify":         adm("verify", map[string]any{"mark": true}),
		"import":         adm("import", map[string]any{"data": `[{"text":"x"}]`}),
		"init":           adm("init", map[string]any{}),
		"demo":           adm("demo", map[string]any{}),
		"attach":         adm("attach", map[string]any{"name": "another"}),
		"detach":         adm("detach", map[string]any{"name": "mine"}),
		"rename":         adm("rename", map[string]any{"old_name": "mine", "new_name": "yours"}),
		"setup":          adm("setup", map[string]any{"sync_dir": "SKILLS"}),
		"graph_from_dir": adm("graph_from_dir", map[string]any{"sync_dirs": "SKILLS"}),
		"plans":          adm("plans", map[string]any{}),
		"outside args": func(string, string) mcpCall {
			return mcpCall{"memgraph_admin", map[string]any{"command": "demo", "dry_run": true}}
		},
	})
}

// A value of the wrong type used to count as absent: a project given as a
// number meant the current repository.
func TestMCPRefusesWrongValues(t *testing.T) {
	refused(t, map[string]func(string, string) mcpCall{
		"project as number": func(id, _ string) mcpCall {
			return mcpCall{"memgraph_delete", map[string]any{"id": id, "project": 123}}
		},
		"tags as string": func(id, _ string) mcpCall {
			return mcpCall{"memgraph_edit", map[string]any{"id": id, "text": "x", "tags": "a,b"}}
		},
		"mark as string": func(string, string) mcpCall { return admin("verify", map[string]any{"mark": "false"}) },
		"limit as string": func(string, string) mcpCall {
			return mcpCall{"memgraph_list", map[string]any{"limit": "5"}}
		},
		"args as string": func(string, string) mcpCall {
			return mcpCall{"memgraph_admin", map[string]any{"command": "demo", "args": `{"project":"mine"}`}}
		},
		"mistyped mode": func(string, string) mcpCall {
			return admin("attach", map[string]any{"name": "another", "mode": "remvoe"})
		},
		"scope without rebind": func(string, string) mcpCall {
			return admin("attach", map[string]any{"name": "another", "from_scope": "x"})
		},
	})
}

// The lists in mcpargs.go must match what the tools advertise and read, or a
// valid call would be refused.
func TestMCPArgumentListsMatchTheTools(t *testing.T) {
	for _, tool := range getMCPTools() {
		var schema struct {
			Properties map[string]any `json:"properties"`
		}
		if err := json.Unmarshal(tool.InputSchema, &schema); err != nil {
			t.Fatal(err)
		}
		advertised := slices.Sorted(maps.Keys(schema.Properties))
		listed := slices.Sorted(maps.Keys(mcpToolParams[tool.Name]))
		if !slices.Equal(advertised, listed) {
			t.Errorf("%s advertises %v, listed %v", tool.Name, advertised, listed)
		}
	}
	listed := map[string]bool{}
	for _, lists := range []map[string]mcpParams{mcpToolParams, mcpAdminParams} {
		for _, params := range lists {
			for name, kind := range params {
				listed[name+" "+string(kind)] = true
			}
		}
	}
	kinds := map[string]mcpType{"String": mcpString, "Bool": mcpBool, "Int": mcpInteger, "StringSlice": mcpStrings}
	sources, err := filepath.Glob("mcp*.go")
	if err != nil {
		t.Fatal(err)
	}
	read := regexp.MustCompile(`mcpGet(String|Bool|Int|StringSlice)\(\w*[aA]rgs, "(\w+)"`)
	for _, source := range sources {
		for _, m := range read.FindAllStringSubmatch(readString(t, source), -1) {
			if key := m[2] + " " + string(kinds[m[1]]); !listed[key] {
				t.Errorf("%s reads %q, which no list has", source, key)
			}
		}
	}
}

// Every listed argument, with a value of its type or null, is accepted.
func TestMCPAcceptsListedArguments(t *testing.T) {
	samples := map[mcpType]any{mcpString: "x", mcpBool: true, mcpInteger: float64(3), mcpStrings: []any{"a"}, mcpObject: map[string]any{}}
	fill := func(params mcpParams, null bool) map[string]any {
		args := map[string]any{}
		for name, kind := range params {
			if !null {
				args[name] = samples[kind]
			} else {
				args[name] = nil
			}
		}
		return args
	}
	for _, null := range []bool{false, true} {
		for tool, params := range mcpToolParams {
			if tool != "memgraph_admin" {
				if err := checkMCPCall(tool, fill(params, null)); err != nil {
					t.Error(err)
				}
			}
		}
		for command, params := range mcpAdminParams {
			args := fill(params, null)
			if command == "attach" && !null {
				args["mode"] = "rebind"
			}
			if err := checkMCPCall("memgraph_admin", map[string]any{"command": command, "args": args}); err != nil {
				t.Error(err)
			}
		}
	}
}
