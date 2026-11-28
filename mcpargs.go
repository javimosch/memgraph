package main

import (
	"fmt"
	"maps"
	"math"
	"slices"
	"strings"
)

type mcpType string

const (
	mcpString  mcpType = "a string"
	mcpBool    mcpType = "true or false"
	mcpInteger mcpType = "a whole number"
	mcpStrings mcpType = "a list of strings"
	mcpObject  mcpType = "an object"
)

type mcpParams map[string]mcpType

// mcpToolParams lists what each tool reads. memgraph_admin takes a command and
// the args of that command, listed in mcpAdminParams.
var mcpToolParams = map[string]mcpParams{
	"memgraph_projects":  {"repair": mcpBool},
	"memgraph_recall":    {"query": mcpString, "project": mcpString, "tags": mcpStrings, "limit": mcpInteger, "format": mcpString},
	"memgraph_read":      {"id": mcpString, "slug": mcpString, "project": mcpString},
	"memgraph_save":      {"text": mcpString, "project": mcpString, "type": mcpString, "tags": mcpStrings},
	"memgraph_list":      {"project": mcpString, "session": mcpString, "tags": mcpStrings, "limit": mcpInteger},
	"memgraph_edit":      {"id": mcpString, "text": mcpString, "project": mcpString, "type": mcpString, "tags": mcpStrings, "session": mcpString},
	"memgraph_delete":    {"id": mcpString, "project": mcpString, "reason": mcpString},
	"memgraph_supersede": {"id": mcpString, "text": mcpString, "with": mcpString, "reason": mcpString, "type": mcpString, "tags": mcpStrings, "project": mcpString},
	"memgraph_recommend": {"task": mcpString, "limit": mcpInteger},
	"memgraph_admin":     {"command": mcpString, "args": mcpObject},
}

var mcpAdminParams = map[string]mcpParams{
	"verify":         {"project": mcpString, "mark": mcpBool, "net": mcpBool, "limit": mcpInteger},
	"ledger":         {"project": mcpString, "since": mcpString, "limit": mcpInteger},
	"sessions":       {"project": mcpString},
	"import":         {"data": mcpString, "project": mcpString, "type": mcpString},
	"init":           {"project": mcpString},
	"status":         {"project": mcpString},
	"config":         {"project": mcpString},
	"profile":        {"project": mcpString, "session": mcpString},
	"attach":         {"name": mcpString, "mode": mcpString, "from_scope": mcpString},
	"detach":         {"name": mcpString, "purge": mcpBool},
	"rename":         {"old_name": mcpString, "new_name": mcpString},
	"demo":           {"project": mcpString},
	"bridge":         {"agent": mcpString},
	"setup":          {"sync_dir": mcpString},
	"feedback":       {"message": mcpString, "kind": mcpString, "context": mcpString},
	"query":          {"keywords": mcpString, "limit": mcpInteger},
	"related":        {"target": mcpString, "limit": mcpInteger},
	"plans":          {},
	"graph_from_dir": {"sync_dirs": mcpStrings, "include_plans": mcpBool},
}

func (t mcpType) holds(value any) bool {
	switch v := value.(type) {
	case string:
		return t == mcpString
	case bool:
		return t == mcpBool
	case float64:
		return t == mcpInteger && v == math.Trunc(v)
	case map[string]any:
		return t == mcpObject
	case []any:
		return t == mcpStrings && !slices.ContainsFunc(v, func(item any) bool {
			_, ok := item.(string)
			return !ok
		})
	}
	return false
}

// checkMCPParams refuses an argument that is not known, or whose value has the
// wrong type. Ignoring one is not safe: it may be an option the caller relies
// on, such as a dry run, and a project that is not a string would mean the
// current repository. A null counts as absent.
func checkMCPParams(what string, params mcpParams, args map[string]any) error {
	for _, name := range slices.Sorted(maps.Keys(args)) {
		want, known := params[name]
		if !known {
			takes := strings.Join(slices.Sorted(maps.Keys(params)), ", ")
			if takes == "" {
				takes = "no arguments"
			}
			return fmt.Errorf("%s has no argument %q, so nothing was done; it takes: %s", what, name, takes)
		}
		if args[name] != nil && !want.holds(args[name]) {
			return fmt.Errorf("%s: %q must be %s, so nothing was done", what, name, want)
		}
	}
	return nil
}

// checkMCPCall checks the arguments of a tool call before the tool runs. An
// unknown tool or admin command is left for the dispatcher to report.
func checkMCPCall(tool string, args map[string]any) error {
	params, ok := mcpToolParams[tool]
	if !ok {
		return nil
	}
	if err := checkMCPParams(tool, params, args); err != nil {
		return err
	}
	if tool != "memgraph_admin" {
		return nil
	}
	command, _ := args["command"].(string)
	params, ok = mcpAdminParams[command]
	if !ok {
		return nil
	}
	sub, _ := args["args"].(map[string]any)
	what := "memgraph_admin " + command
	if err := checkMCPParams(what, params, sub); err != nil {
		return err
	}
	if command == "attach" {
		mode, _ := sub["mode"].(string)
		if !slices.Contains([]string{"", "register", "remove", "rebind"}, mode) {
			return fmt.Errorf("%s: mode must be register, remove or rebind, so nothing was done", what)
		}
		if from, _ := sub["from_scope"].(string); from != "" && mode != "rebind" {
			return fmt.Errorf("%s: from_scope only goes with mode rebind, so nothing was done", what)
		}
	}
	return nil
}
