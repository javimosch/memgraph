package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var namespaceRe = regexp.MustCompile(`^([a-z]+)-`)

func ensureMemoryDir(memoryPath string) error {
	if err := os.MkdirAll(memoryPath, 0755); err != nil {
		return err
	}
	indexPath := filepath.Join(memoryPath, "MEMORY.md")
	if _, err := os.Stat(indexPath); os.IsNotExist(err) {
		content := "# Memory Index\nThis file contains pointers to all project memories.\nLast updated: " + time.Now().Format(time.RFC3339) + "\n\n"
		if err := os.WriteFile(indexPath, []byte(content), 0644); err != nil {
			return err
		}
	}
	return nil
}

func unquote(s string) string {
	if len(s) >= 2 && ((s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'')) {
		return s[1 : len(s)-1]
	}
	return s
}

func parseMinimalFrontmatter(content string) (name, description, body string) {
	lines := strings.Split(content, "\n")
	inFrontmatter := false
	frontmatterClosed := false
	nameSet := false
	descSet := false
	var bodyLines []string

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !frontmatterClosed && trimmed == "---" {
			if !inFrontmatter {
				inFrontmatter = true
				continue
			}
			frontmatterClosed = true
			inFrontmatter = false
			continue
		}

		if inFrontmatter {
			idx := strings.Index(line, ":")
			if idx > 0 {
				key := strings.TrimSpace(line[:idx])
				value := unquote(strings.TrimSpace(line[idx+1:]))
				switch key {
				case "name":
					if !nameSet {
						name = value
						nameSet = true
					}
				case "description":
					if !descSet {
						description = value
						descSet = true
					}
				}
			}
			continue
		}

		bodyLines = append(bodyLines, line)
	}

	if !frontmatterClosed {
		bodyLines = lines
	}
	body = strings.Join(bodyLines, "\n")
	return
}

func firstNonEmptyLine(body string) string {
	for _, line := range strings.Split(body, "\n") {
		t := strings.TrimSpace(line)
		if t != "" && t != "---" {
			return t
		}
	}
	return ""
}

func deriveNamespace(dirName, fallback string) string {
	matches := namespaceRe.FindStringSubmatch(dirName)
	if len(matches) > 1 {
		return matches[1]
	}
	if fallback != "" {
		return fallback
	}
	return "general"
}

func makeSkillTags(dirName, project string) []string {
	seen := make(map[string]bool)
	var tags []string
	for _, part := range strings.Split(dirName, "-") {
		p := strings.TrimSpace(strings.ToLower(part))
		if p == "" || seen[p] {
			continue
		}
		tags = append(tags, p)
		seen[p] = true
	}
	if project != "" && !seen[project] {
		tags = append(tags, project)
		seen[project] = true
	}
	if !seen["skill"] {
		tags = append(tags, "skill")
	}
	return tags
}

func makeSkillID(dirName string) string {
	return sanitizePath(strings.ToLower(dirName))
}

func scanSkillFiles(dir string) ([]skillInput, error) {
	var skills []skillInput
	seenIDs := make(map[string]bool)

	err := filepath.WalkDir(dir, func(path string, info fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if info.Type()&os.ModeSymlink != 0 {
			return nil
		}
		if info.IsDir() {
			return nil
		}

		name := info.Name()
		var skill skillInput
		if name == "SKILL.md" {
			skill, err = parseSkillFile(path)
		} else if strings.HasSuffix(name, ".md") {
			skill, err = parseMarkdownFile(path)
		} else {
			return nil
		}
		if err != nil {
			return nil
		}
		if seenIDs[skill.ID] {
			return nil
		}
		seenIDs[skill.ID] = true
		skills = append(skills, skill)
		return nil
	})

	return skills, err
}

func parseMarkdownFile(path string) (skillInput, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return skillInput{}, err
	}

	info, err := os.Stat(path)
	modTime := time.Now().UTC()
	if err == nil {
		modTime = info.ModTime().UTC()
	}

	name, description, body := parseMinimalFrontmatter(string(data))
	fileName := strings.TrimSuffix(filepath.Base(path), ".md")
	dirName := fileName

	id := makeSkillID(dirName)
	if name == "" {
		name = fileName
	}
	if description == "" {
		description = firstNonEmptyLine(body)
	}
	if description == "" {
		description = "No description"
	}

	return skillInput{
		ID:          id,
		DirName:     dirName,
		Name:        name,
		Description: description,
		Content:     body,
		Created:     modTime,
		FilePath:    path,
	}, nil
}

func parseSkillFile(path string) (skillInput, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return skillInput{}, err
	}

	info, err := os.Stat(path)
	modTime := time.Now().UTC()
	if err == nil {
		modTime = info.ModTime().UTC()
	}

	name, description, body := parseMinimalFrontmatter(string(data))
	dirName := filepath.Base(filepath.Dir(path))
	if dirName == "." {
		dirName = ""
	}

	id := makeSkillID(dirName)
	if name == "" {
		name = dirName
	}
	if description == "" {
		description = firstNonEmptyLine(body)
	}
	if description == "" {
		description = "No description"
	}

	return skillInput{
		ID:          id,
		DirName:     dirName,
		Name:        name,
		Description: description,
		Content:     body,
		Created:     modTime,
		FilePath:    path,
	}, nil
}

func ingestSkillsDir(sourceDir, targetDir, fallbackProject string, cfg *Config) (*skillGraph, int, int, error) {
	if err := ensureMemoryDir(targetDir); err != nil {
		return nil, 0, 0, fmt.Errorf("failed to initialize memory directory: %w", err)
	}

	skills, err := scanSkillFiles(sourceDir)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("failed to scan directory: %w", err)
	}
	if len(skills) == 0 {
		return nil, 0, 0, fmt.Errorf("no markdown files found in %s", sourceDir)
	}

	for i := range skills {
		skills[i].Project = deriveNamespace(skills[i].DirName, fallbackProject)
		skills[i].Tags = makeSkillTags(skills[i].DirName, skills[i].Project)
	}

	return writeSkillGraph(skills, targetDir, cfg)
}

func ingestMultiDir(sourceDirs []string, targetDir string, cfg *Config) (*skillGraph, int, int, error) {
	if err := ensureMemoryDir(targetDir); err != nil {
		return nil, 0, 0, fmt.Errorf("failed to initialize memory directory: %w", err)
	}

	// Clear old memory files from previous syncs
	entries, _ := os.ReadDir(targetDir)
	for _, e := range entries {
		if !e.IsDir() && strings.HasPrefix(e.Name(), "memory_") && strings.HasSuffix(e.Name(), ".md") {
			_ = os.Remove(filepath.Join(targetDir, e.Name()))
		}
	}

	var allSkills []skillInput
	seenIDs := make(map[string]bool)
	for _, dir := range sourceDirs {
		skills, err := scanSkillFiles(dir)
		if err != nil {
			continue
		}
		for _, s := range skills {
			if seenIDs[s.ID] {
				continue
			}
			seenIDs[s.ID] = true
			allSkills = append(allSkills, s)
		}
	}

	if len(allSkills) == 0 {
		return nil, 0, 0, fmt.Errorf("no markdown files found in any source directory")
	}

	for i := range allSkills {
		allSkills[i].Project = deriveNamespace(allSkills[i].DirName, "")
		allSkills[i].Tags = makeSkillTags(allSkills[i].DirName, allSkills[i].Project)
	}

	return writeSkillGraph(allSkills, targetDir, cfg)
}

func writeSkillGraph(skills []skillInput, targetDir string, cfg *Config) (*skillGraph, int, int, error) {
	graph := buildSkillGraph(skills)

	namespaceCount := 0
	skillCount := 0
	for _, node := range graph.Nodes {
		outPath := filepath.Join(targetDir, "memory_"+node.ID+".md")
		if err := os.WriteFile(outPath, []byte(formatMemoryFile(node)), 0644); err != nil {
			return nil, 0, 0, fmt.Errorf("failed to write memory file: %w", err)
		}
		if node.Type == "namespace" {
			namespaceCount++
		} else {
			skillCount++
		}
	}

	if err := saveGraphIndex(targetDir, graph); err != nil {
		return nil, 0, 0, fmt.Errorf("failed to write graph index: %w", err)
	}

	if cfg != nil {
		updateSearchIndex(cfg)
	}

	return graph, skillCount, namespaceCount, nil
}

func handleGraphFromDir(cfg *Config) {
	positional, opts := parseCommandArgs(os.Args[2:])
	if len(positional) < 1 {
		errorResponse(80, "missing_argument", "Usage: memgraph graph-from-dir <dir> [--memory-dir <target>] [--project <project>]", false)
		os.Exit(80)
	}

	sourceDir := positional[0]
	if _, err := os.Stat(sourceDir); os.IsNotExist(err) {
		errorResponse(92, "resource_error", fmt.Sprintf("Directory not found: %s", sourceDir), false)
		os.Exit(92)
	}

	targetDir := cfg.MemoryDir
	if memoryDir == "" {
		targetDir = filepath.Join(getGlobalMemgraphDir(), "skills-graph")
	}
	cfg.MemoryDir = targetDir

	fallbackProject := ""
	if opts.ProjectSet {
		fallbackProject = opts.Project
	}

	graph, skillCount, namespaceCount, err := ingestSkillsDir(sourceDir, targetDir, fallbackProject, cfg)
	if err != nil {
		errorResponse(92, "resource_error", err.Error(), false)
		os.Exit(92)
	}

	if jsonOutput {
		successResponse(map[string]interface{}{
			"source":     sourceDir,
			"target":     targetDir,
			"skills":     skillCount,
			"namespaces": namespaceCount,
			"nodes":      len(graph.Nodes),
			"edges":      len(graph.Edges),
		})
	} else {
		fmt.Printf("Ingested %d skills from %s into %s\n", skillCount, sourceDir, targetDir)
		fmt.Printf("Namespaces: %d, Total nodes: %d, Edges: %d\n", namespaceCount, len(graph.Nodes), len(graph.Edges))
	}
}
