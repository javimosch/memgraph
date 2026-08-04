package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Planning-with-files integration: index task_plan.md, findings.md, and
// progress.md files as graph nodes with type "plan". This gives memgraph
// cross-session memory — "last time you fixed a websocket bug, here's what
// you tried and what worked."
//
// Inspired by and compatible with planning-with-files (26k stars):
// https://github.com/OthmanAdi/planning-with-files

// planInput is the parsed representation of a planning-with-files task.
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
}

// scanPlanFiles discovers task_plan.md files in the given directory tree.
// It looks for:
//   - task_plan.md in the root of each directory (legacy mode)
//   - .planning/*/task_plan.md (parallel task mode, v2.36.0+)
//
// For each task_plan.md found, it also reads findings.md and progress.md
// from the same directory to build a richer node.
func scanPlanFiles(dir string) ([]planInput, error) {
	var plans []planInput
	seenDirs := make(map[string]bool)

	resolvedDir := expandHomeDir(dir)

	// 1. Look for task_plan.md in the root
	rootPlan := filepath.Join(resolvedDir, "task_plan.md")
	if _, err := os.Stat(rootPlan); err == nil {
		if plan, err := parsePlanFile(resolvedDir, rootPlan); err == nil {
			plans = append(plans, plan)
			seenDirs[resolvedDir] = true
		}
	}

	// 2. Look for .planning/*/task_plan.md (parallel tasks)
	planningDir := filepath.Join(resolvedDir, ".planning")
	entries, err := os.ReadDir(planningDir)
	if err == nil {
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			planPath := filepath.Join(planningDir, entry.Name(), "task_plan.md")
			if _, err := os.Stat(planPath); err == nil {
				planDir := filepath.Join(planningDir, entry.Name())
				if seenDirs[planDir] {
					continue
				}
				if plan, err := parsePlanFile(planDir, planPath); err == nil {
					plans = append(plans, plan)
					seenDirs[planDir] = true
				}
			}
		}
	}

	// 3. Walk subdirectories (max depth 3) to find task_plan.md in project dirs
	// This catches plans in nested project directories
	filepath.WalkDir(resolvedDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return nil
		}
		// Skip hidden dirs (except .planning, already handled) and common noise
		name := d.Name()
		if strings.HasPrefix(name, ".") && name != ".planning" {
			return filepath.SkipDir
		}
		if name == "node_modules" || name == ".git" || name == "vendor" {
			return filepath.SkipDir
		}
		// Limit depth
		rel, _ := filepath.Rel(resolvedDir, path)
		depth := strings.Count(rel, string(filepath.Separator))
		if depth > 3 {
			return filepath.SkipDir
		}
		planPath := filepath.Join(path, "task_plan.md")
		if _, err := os.Stat(planPath); err == nil {
			if !seenDirs[path] {
				if plan, err := parsePlanFile(path, planPath); err == nil {
					plans = append(plans, plan)
					seenDirs[path] = true
				}
			}
		}
		return nil
	})

	return plans, nil
}

// parsePlanFile reads a task_plan.md and its sibling findings.md/progress.md
// to build a planInput.
func parsePlanFile(planDir, planPath string) (planInput, error) {
	planData, err := os.ReadFile(planPath)
	if err != nil {
		return planInput{}, err
	}
	planText := string(planData)

	// Extract name: first heading or first non-empty line
	name := extractPlanName(planText)

	// Extract description: the goal line (first paragraph after any heading)
	description := extractPlanDescription(planText)

	// Determine status from checkboxes
	status := extractPlanStatus(planText)

	// Read findings.md for content (research notes)
	content := ""
	findingsPath := filepath.Join(planDir, "findings.md")
	if findingsData, err := os.ReadFile(findingsPath); err == nil {
		content = string(findingsData)
		// Truncate to 1000 chars (same as skill content)
		if len(content) > 1000 {
			content = content[:1000]
		}
	}

	// If no findings.md, use the plan text itself
	if content == "" {
		content = planText
		if len(content) > 1000 {
			content = content[:1000]
		}
	}

	// Derive project from parent directory name
	project := filepath.Base(filepath.Dir(planDir))
	if project == "" || project == "." {
		project = "plans"
	}

	// Generate ID from directory path
	planID := sanitizePath(strings.ToLower(filepath.Base(planDir)))
	if !strings.HasPrefix(planID, "plan-") {
		planID = "plan-" + planID
	}

	// Extract skill references from plan + findings + progress


	tags := []string{"planning", "task-plan"}
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
	}, nil
}

// extractPlanName gets the first heading from a task_plan.md.
func extractPlanName(text string) string {
	lines := strings.Split(text, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "# ") {
			return strings.TrimPrefix(line, "# ")
		}
	}
	// Fallback: first non-empty, non-marker line
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "---") || strings.HasPrefix(line, "===") {
			continue
		}
		return line
	}
	return "unnamed-plan"
}

// extractPlanDescription gets the goal line — first paragraph after the title.
func extractPlanDescription(text string) string {
	lines := strings.Split(text, "\n")
	pastHeading := false
	var descLines []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "# ") {
			pastHeading = true
			continue
		}
		if pastHeading && trimmed != "" && !strings.HasPrefix(trimmed, "##") && !strings.HasPrefix(trimmed, "- [") {
			descLines = append(descLines, trimmed)
			if len(descLines) >= 2 {
				break
			}
		}
	}
	if len(descLines) == 0 {
		return "Planning task"
	}
	desc := strings.Join(descLines, " ")
	if len(desc) > 120 {
		desc = desc[:120] + "..."
	}
	return desc
}

// extractPlanStatus checks if all checkboxes are checked.
func extractPlanStatus(text string) string {
	total := 0
	checked := 0
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "- [x]") || strings.HasPrefix(trimmed, "- [X]") {
			total++
			checked++
		} else if strings.HasPrefix(trimmed, "- [ ]") {
			total++
		}
	}
	if total == 0 {
		return "in_progress"
	}
	if checked == total {
		return "complete"
	}
	return "in_progress"
}

// extractSkillRefs finds skill names mentioned in the text.
// Skill names are typically kebab-case identifiers like "audit-website" or
// "beads-tracker" that appear in findings or progress files.
func extractSkillRefs(text string) []string {
	var refs []string
	seen := make(map[string]bool)

	// Match patterns like "skill: audit-website" or "used audit-website" or
	// just kebab-case words that look like skill names (3+ chars, contain a hyphen)
	words := strings.Fields(text)
	for _, w := range words {
		w = strings.Trim(w, "`*[](){}\"'")
		if strings.Contains(w, "-") && len(w) >= 5 && len(w) <= 40 {
			// Check it looks like a skill name: lowercase, hyphens, no spaces
			if isKebabCase(w) && !seen[w] {
				seen[w] = true
				refs = append(refs, w)
			}
		}
	}
	return refs
}

func isKebabCase(s string) bool {
	for _, c := range s {
		if !((c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-') {
			return false
		}
	}
	return true
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

// discoverPlanDirs finds directories that might contain planning-with-files
// task_plan.md files. Scans common project locations.
func discoverPlanDirs() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	candidates := []string{
		filepath.Join(home, "ai"),        // ~/ai/* projects
		filepath.Join(home, "projets"),   // ~/projets/* projects
		filepath.Join(home, "projects"),  // ~/projects/* (common)
		filepath.Join(home, "repos"),     // ~/repos/* (common)
		filepath.Join(home, "pr"),        // ~/pr/* (worktrees)
	}
	var dirs []string
	for _, d := range candidates {
		if info, err := os.Stat(d); err == nil && info.IsDir() {
			dirs = append(dirs, d)
		}
	}
	return dirs
}

// handlePlansList lists all indexed plans in the graph.
func handlePlansList(cfg *Config) {
	graph, err := loadGraphIndex(cfg.MemoryDir)
	if err != nil {
		errorResponse(92, "resource_error", "Failed to load graph: "+err.Error(), false)
		os.Exit(92)
	}

	var plans []Memory
	for _, node := range graph.Nodes {
		if node.Type == "plan" {
			plans = append(plans, node)
		}
	}

	if jsonOutput {
		fmt.Printf("{\"plans\":%d,\"items\":[", len(plans))
		for i, p := range plans {
			if i > 0 {
				fmt.Print(",")
			}
			fmt.Printf(`{"id":"%s","name":"%s","project":"%s","description":"%s"}`,
				p.ID, p.Name, p.Project, truncate(p.Description, 80))
		}
		fmt.Println("]}")
	} else {
		if len(plans) == 0 {
			fmt.Println("No plans indexed. Run: memgraph graph-from-dir --include-plans")
			return
		}
		fmt.Printf("Indexed plans (%d):\n", len(plans))
		for _, p := range plans {
			fmt.Printf("  %s  %s  [%s]\n", p.ID, p.Name, p.Project)
		}
	}
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}

// ingestPlans discovers planning-with-files task_plan.md files in common
// project directories, converts them to skillInput nodes (type "plan"), and
// appends them to the existing graph. Returns the number of plans indexed.
// If extraDirs is provided, those directories are also scanned.
func ingestPlans(targetDir string, cfg *Config, extraDirs ...string) int {
	planDirs := discoverPlanDirs()
	planDirs = append(planDirs, extraDirs...)
	if len(planDirs) == 0 {
		return 0
	}

	var allPlans []planInput
	seenIDs := make(map[string]bool)
	for _, dir := range planDirs {
		plans, err := scanPlanFiles(dir)
		if err != nil {
			continue
		}
		for _, p := range plans {
			if seenIDs[p.ID] {
				continue
			}
			seenIDs[p.ID] = true
			allPlans = append(allPlans, p)
		}
	}

	if len(allPlans) == 0 {
		return 0
	}

	// Convert to skillInput and merge with existing graph
	planInputs := plansToSkillInputs(allPlans)

	// Load existing graph, append plan nodes, save
	graph, err := loadGraphIndex(targetDir)
	if err != nil {
		return 0
	}

	for _, pi := range planInputs {
		node := Memory{
			ID:          pi.ID,
			Name:        pi.Name,
			Description: pi.Description,
			Type:        "plan",
			Project:     pi.Project,
			Tags:        pi.Tags,
			Created:     pi.Created,
			Content:     pi.Content,
			FilePath:    pi.FilePath,
		}
		graph.Nodes = append(graph.Nodes, node)
	}

	// Save the updated graph
	if err := saveGraphIndex(targetDir, &skillGraph{
		Nodes: graph.Nodes,
		Edges: graph.Edges,
	}); err != nil {
		return 0
	}
	_ = planInputs // planInputs used above via the loop

	if cfg != nil {
		updateSearchIndex(cfg)
	}

	return len(allPlans)
}
