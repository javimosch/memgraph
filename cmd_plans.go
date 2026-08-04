package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// shortHash returns the first n characters of a SHA-256 hex hash of the input.
func shortHash(s string, n int) string {
	h := sha256.Sum256([]byte(s))
	hex := hex.EncodeToString(h[:])
	if len(hex) > n {
		hex = hex[:n]
	}
	return hex
}

// Generic plan file discovery: indexes task plans, TODOs, and roadmaps from
// any planning framework — not just planning-with-files.
//
// Discovery uses three layers:
//  1. Built-in patterns (task_plan.md, TODO.md, PLAN.md, docs/plan/*.md, ...)
//  2. User config (~/.memgraph/plan-patterns.json) for custom patterns
//  3. Auto-detect heuristic: any .md file with phase/checkbox structure
//
// Compatible with planning-with-files (https://github.com/OthmanAdi/planning-with-files)
// as one of the built-in patterns, not a special case.

// planPattern defines how to recognize and parse a planning file.
type planPattern struct {
	Name         string   `json:"name"`          // pattern identifier (e.g. "planning-with-files")
	Trigger      string   `json:"trigger"`       // filename that signals a plan (e.g. "task_plan.md")
	ContentFiles []string `json:"content_files"` // sibling files to read for rich content
	Subdirs      string   `json:"subdirs"`       // subdir pattern for parallel plans (e.g. ".planning/*")
	Source       string   `json:"source"`        // "builtin" or "config"
}

// planInput is the parsed representation of a discovered plan.
type planInput struct {
	ID          string
	DirName     string
	Name        string
	Description string
	Project     string
	Content     string
	Tags        []string
	Created     time.Time
	FilePath    string
	Status      string // "complete", "in_progress"
	Pattern     string // which pattern matched
}

// builtinPlanPatterns returns the built-in set of known plan file patterns.
// These cover common conventions across different planning frameworks and
// personal repo layouts.
func builtinPlanPatterns() []planPattern {
	return []planPattern{
		{
			Name:         "planning-with-files",
			Trigger:      "task_plan.md",
			ContentFiles: []string{"findings.md", "progress.md"},
			Subdirs:      ".planning",
			Source:       "builtin",
		},
		{
			Name:         "todo-md",
			Trigger:      "TODO.md",
			ContentFiles: nil,
			Subdirs:      "",
			Source:       "builtin",
		},
		{
			Name:         "plan-md",
			Trigger:      "PLAN.md",
			ContentFiles: nil,
			Subdirs:      "",
			Source:       "builtin",
		},
		{
			Name:         "roadmap-md",
			Trigger:      "ROADMAP.md",
			ContentFiles: nil,
			Subdirs:      "",
			Source:       "builtin",
		},
		{
			Name:         "docs-plan",
			Trigger:      "plan.md",
			ContentFiles: nil,
			Subdirs:      "docs/plan",
			Source:       "builtin",
		},
		{
			Name:         "docs-plans",
			Trigger:      "plans.md",
			ContentFiles: nil,
			Subdirs:      "docs/plans",
			Source:       "builtin",
		},
	}
}

// loadPlanPatterns loads built-in patterns + user config from
// ~/.memgraph/plan-patterns.json. Config patterns are merged on top of
// built-ins (same Name overrides builtin).
func loadPlanPatterns() []planPattern {
	patterns := builtinPlanPatterns()

	home, err := os.UserHomeDir()
	if err != nil {
		return patterns
	}
	configPath := filepath.Join(home, ".memgraph", "plan-patterns.json")
	data, err := os.ReadFile(configPath)
	if err != nil {
		return patterns
	}

	var custom []planPattern
	if err := json.Unmarshal(data, &custom); err != nil {
		return patterns
	}

	// Merge: custom overrides builtin by Name
	for _, c := range custom {
		c.Source = "config"
		found := false
		for i, p := range patterns {
			if p.Name == c.Name {
				patterns[i] = c
				found = true
				break
			}
		}
		if !found {
			patterns = append(patterns, c)
		}
	}
	return patterns
}

// scanPlanFiles discovers plan files in the given directory tree using all
// loaded patterns. It also runs an auto-detect heuristic for .md files that
// aren't matched by any known pattern but look like plans (have phases,
// checkboxes, etc.).
func scanPlanFiles(dir string) ([]planInput, error) {
	patterns := loadPlanPatterns()
	var plans []planInput
	seenFiles := make(map[string]bool)

	resolvedDir := expandHomeDir(dir)

	// 1. Pattern-based discovery
	for _, p := range patterns {
		patternPlans := scanWithPattern(resolvedDir, p)
		for _, plan := range patternPlans {
			if !seenFiles[plan.FilePath] {
				seenFiles[plan.FilePath] = true
				plans = append(plans, plan)
			}
		}
	}

	// 2. Auto-detect: walk for .md files that look like plans but aren't
	// matched by any known pattern. Scores based on structure.
	filepath.WalkDir(resolvedDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		name := d.Name()
		if !strings.HasSuffix(name, ".md") {
			return nil
		}
		if seenFiles[path] {
			return nil
		}
		// Skip noise
		if name == "README.md" || name == "CHANGELOG.md" || name == "LICENSE.md" ||
			name == "CONTRIBUTING.md" || name == "AGENTS.md" || name == "CLAUDE.md" {
			return nil
		}
		// Skip SKILL.md (already indexed as skills)
		if name == "SKILL.md" {
			return nil
		}
		// Read and score
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		text := string(data)
		score := scorePlanLikelihood(text)
		if score >= 4 {
			plan := parsePlanWithPattern(filepath.Dir(path), path, planPattern{
				Name:   "auto-detect",
				Source: "heuristic",
			})
			plan.Pattern = "auto-detect"
			plans = append(plans, plan)
			seenFiles[path] = true
		}
		return nil
	})

	return plans, nil
}

// scanWithPattern scans a directory tree for files matching a single pattern.
func scanWithPattern(rootDir string, p planPattern) []planInput {
	var plans []planInput

	// If the pattern has a subdirs field (e.g. ".planning" or "docs/plan"),
	// scan those subdirectories for the trigger file
	if p.Subdirs != "" {
		subdirPath := filepath.Join(rootDir, p.Subdirs)
		entries, err := os.ReadDir(subdirPath)
		if err == nil {
			for _, entry := range entries {
				if !entry.IsDir() {
					continue
				}
				triggerPath := filepath.Join(subdirPath, entry.Name(), p.Trigger)
				if _, err := os.Stat(triggerPath); err == nil {
					planDir := filepath.Join(subdirPath, entry.Name())
					plan := parsePlanWithPattern(planDir, triggerPath, p)
					plans = append(plans, plan)
				}
			}
		}
	}

	// Walk the tree for the trigger file at any depth (max 4)
	filepath.WalkDir(rootDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			name := d.Name()
			// Skip hidden dirs and common noise
			if strings.HasPrefix(name, ".") && name != ".planning" {
				return filepath.SkipDir
			}
			if name == "node_modules" || name == ".git" || name == "vendor" {
				return filepath.SkipDir
			}
			// Limit depth
			rel, _ := filepath.Rel(rootDir, path)
			depth := strings.Count(rel, string(filepath.Separator))
			if depth > 4 {
				return filepath.SkipDir
			}
			return nil
		}
		// Check if this file matches the trigger
		if d.Name() == p.Trigger {
			// For docs/plan and docs/plans patterns, only match inside the
			// expected directory to avoid false positives on common filenames
			if p.Name == "docs-plan" || p.Name == "docs-plans" {
				parent := filepath.Base(filepath.Dir(path))
				expectedParent := strings.TrimSuffix(p.Subdirs, "s")
				if parent != "plan" && parent != "plans" {
					return nil
				}
				_ = expectedParent
			}
			planDir := filepath.Dir(path)
			plan := parsePlanWithPattern(planDir, path, p)
			plans = append(plans, plan)
		}
		return nil
	})

	return plans
}

// parsePlanWithPattern reads a plan file and its sibling content files
// (as defined by the pattern) to build a planInput.
func parsePlanWithPattern(planDir, planPath string, p planPattern) planInput {
	planData, err := os.ReadFile(planPath)
	if err != nil {
		return planInput{}
	}
	planText := string(planData)

	name := extractPlanName(planText)
	description := extractPlanDescription(planText)
	status := extractPlanStatus(planText)

	// Read content files defined by the pattern
	content := ""
	for _, cf := range p.ContentFiles {
		cfPath := filepath.Join(planDir, cf)
		if cfData, err := os.ReadFile(cfPath); err == nil {
			content += string(cfData) + "\n"
		}
	}

	// If no content files or empty, use the plan text itself
	if content == "" {
		content = planText
	}
	if len(content) > 1000 {
		content = content[:1000]
	}

	// Derive project from parent directory name
	project := filepath.Base(filepath.Dir(planDir))
	if project == "" || project == "." {
		project = "plans"
	}

	// Generate a unique ID from the full file path to avoid collisions
	// between plans with the same directory name in different projects
	planID := "plan-" + sanitizePath(strings.ToLower(filepath.Base(planDir)))
	// Append a short hash of the full path for uniqueness
	pathHash := shortHash(planPath, 8)
	planID = planID + "-" + pathHash

	tags := []string{"planning", "plan"}
	if p.Name != "auto-detect" {
		tags = append(tags, p.Name)
	}
	if status == "complete" {
		tags = append(tags, "completed")
	}

	info, _ := os.Stat(planPath)

	return planInput{
		ID:          planID,
		DirName:     filepath.Base(planDir),
		Name:        name,
		Description: description,
		Project:     project,
		Content:     content,
		Tags:        tags,
		Created:     info.ModTime(),
		FilePath:    planPath,
		Status:      status,
		Pattern:     p.Name,
	}
}

// plansToSkillInputs converts planInputs to skillInputs so they can be
// ingested alongside skills in the same graph.
func plansToSkillInputs(plans []planInput) []skillInput {
	var inputs []skillInput
	for _, p := range plans {
		inputs = append(inputs, skillInput{
			ID:          p.ID,
			DirName:     p.DirName,
			Name:        p.Name,
			Description: p.Description,
			Project:     p.Project,
			Content:     p.Content,
			Tags:        p.Tags,
			Created:     p.Created,
			FilePath:    p.FilePath,
		})
	}
	return inputs
}

// discoverPlanDirs is in cmd_plans_ingest.go
// handlePlansList is in cmd_plans_ingest.go
// ingestPlans is in cmd_plans_ingest.go
// truncate is in cmd_plans_ingest.go
