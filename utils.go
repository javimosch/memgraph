package main

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

type CommandOptions struct {
	MemoryType  string
	Project     string
	ProjectSet  bool
	Tags        []string
	TagsSet     bool
	TagOnly     bool
	WeightsJSON string
	Limit       int
	Port        int
	Text        string
	TextSet     bool
	Query       string
	QuerySet    bool
	Session     string
	SessionSet  bool
	SyncDir     string
	AutoSync    bool
	PollInterval int
	IncludePlans bool
	Format      string
	FormatSet   bool
	FromScope    string
	RemoveAttach bool
	AttachName   string
}

func parseCommandArgs(args []string) ([]string, CommandOptions) {
	var positional []string
	var opts CommandOptions

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--json" || arg == "-j" || arg == "--no-interactive" || arg == "-y":
			continue
		case arg == "--memory-dir":
			if i+1 < len(args) {
				i++
			}
			continue
		case strings.HasPrefix(arg, "--memory-dir="):
			continue
		case arg == "--type":
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "--") {
				opts.MemoryType = args[i+1]
				i++
			}
			continue
		case strings.HasPrefix(arg, "--type="):
			opts.MemoryType = strings.TrimPrefix(arg, "--type=")
			continue
		case arg == "--project":
			opts.ProjectSet = true
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "--") {
				opts.Project = args[i+1]
				i++
			}
			continue
		case strings.HasPrefix(arg, "--project="):
			opts.ProjectSet = true
			opts.Project = strings.TrimPrefix(arg, "--project=")
			continue
		case arg == "--tags":
			opts.TagsSet = true
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "--") {
				opts.Tags = parseTagsValue(args[i+1])
				i++
			}
			continue
		case strings.HasPrefix(arg, "--tags="):
			opts.TagsSet = true
			opts.Tags = parseTagsValue(strings.TrimPrefix(arg, "--tags="))
			continue
		case arg == "--limit":
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "--") {
				if n, err := strconv.Atoi(args[i+1]); err == nil && n >= 0 {
					opts.Limit = n
				}
				i++
			}
			continue
		case strings.HasPrefix(arg, "--limit="):
			if n, err := strconv.Atoi(strings.TrimPrefix(arg, "--limit=")); err == nil && n >= 0 {
				opts.Limit = n
			}
			continue
		case arg == "--port":
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "--") {
				if n, err := strconv.Atoi(args[i+1]); err == nil && n > 0 {
					opts.Port = n
				}
				i++
			}
			continue
		case strings.HasPrefix(arg, "--port="):
			if n, err := strconv.Atoi(strings.TrimPrefix(arg, "--port=")); err == nil && n > 0 {
				opts.Port = n
			}
			continue
		case arg == "--text":
			opts.TextSet = true
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "--") {
				opts.Text = args[i+1]
				i++
			}
			continue
		case strings.HasPrefix(arg, "--text="):
			opts.TextSet = true
			opts.Text = strings.TrimPrefix(arg, "--text=")
			continue
		case arg == "--query":
			opts.QuerySet = true
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "--") {
				opts.Query = args[i+1]
				i++
			}
			continue
		case strings.HasPrefix(arg, "--query="):
			opts.QuerySet = true
			opts.Query = strings.TrimPrefix(arg, "--query=")
			continue
		case arg == "--session":
			opts.SessionSet = true
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "--") {
				opts.Session = args[i+1]
				i++
			}
			continue
		case strings.HasPrefix(arg, "--session="):
			opts.SessionSet = true
			opts.Session = strings.TrimPrefix(arg, "--session=")
			continue
		case arg == "--sync-dir":
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "--") {
				opts.SyncDir = args[i+1]
				i++
			}
			continue
		case strings.HasPrefix(arg, "--sync-dir="):
			opts.SyncDir = strings.TrimPrefix(arg, "--sync-dir=")
			continue
		case arg == "--auto-sync":
			opts.AutoSync = true
			continue
		case arg == "--poll-interval":
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "--") {
				fmt.Sscanf(args[i+1], "%d", &opts.PollInterval)
				i++
			}
			continue
		case strings.HasPrefix(arg, "--poll-interval="):
			fmt.Sscanf(strings.TrimPrefix(arg, "--poll-interval="), "%d", &opts.PollInterval)
			continue
		case arg == "--include-plans":
			opts.IncludePlans = true
			continue
		case arg == "--format":
			opts.FormatSet = true
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "--") {
				opts.Format = args[i+1]
				i++
			}
			continue
		case strings.HasPrefix(arg, "--format="):
			opts.FormatSet = true
			opts.Format = strings.TrimPrefix(arg, "--format=")
			continue
		case arg == "--tag-only":
			opts.TagOnly = true
			continue
		case arg == "--weights":
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "--") {
				opts.WeightsJSON = args[i+1]
				i++
			}
			continue
		case strings.HasPrefix(arg, "--weights="):
			opts.WeightsJSON = strings.TrimPrefix(arg, "--weights=")
			continue
		case arg == "--from-scope":
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "--") {
				opts.FromScope = args[i+1]
				i++
			}
			continue
		case strings.HasPrefix(arg, "--from-scope="):
			opts.FromScope = strings.TrimPrefix(arg, "--from-scope=")
			continue
		case arg == "--remove":
			opts.RemoveAttach = true
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "--") {
				opts.AttachName = args[i+1]
				i++
			}
			continue
		case strings.HasPrefix(arg, "--remove="):
			opts.RemoveAttach = true
			opts.AttachName = strings.TrimPrefix(arg, "--remove=")
			continue
		case arg == "--attach-name":
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "--") {
				opts.AttachName = args[i+1]
				i++
			}
			continue
		case strings.HasPrefix(arg, "--attach-name="):
			opts.AttachName = strings.TrimPrefix(arg, "--attach-name=")
			continue
		}
		positional = append(positional, arg)
	}
	return positional, opts
}

func parseTagsValue(value string) []string {
	if value == "" {
		return []string{}
	}
	parts := strings.Split(value, ",")
	var tags []string
	for _, p := range parts {
		t := strings.TrimSpace(p)
		if t != "" {
			tags = append(tags, t)
		}
	}
	return tags
}

func parseSearchWeightsJSON(s string) (SearchWeights, bool) {
	var w SearchWeights
	if err := json.Unmarshal([]byte(s), &w); err != nil {
		return SearchWeights{}, false
	}
	return w, true
}

func mergeSearchWeights(base, override SearchWeights) SearchWeights {
	if override.TFIDF != 0 {
		base.TFIDF = override.TFIDF
	}
	if override.Phrase != 0 {
		base.Phrase = override.Phrase
	}
	if override.Exact != 0 {
		base.Exact = override.Exact
	}
	if override.Recency24h != 0 {
		base.Recency24h = override.Recency24h
	}
	if override.Recency7d != 0 {
		base.Recency7d = override.Recency7d
	}
	if override.Type != 0 {
		base.Type = override.Type
	}
	if override.Tag != 0 {
		base.Tag = override.Tag
	}
	return base
}
