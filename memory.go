package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// slugRe matches lines like "[p22-diagnostics]" at the start of a content line.
var slugRe = regexp.MustCompile(`^\[([a-zA-Z0-9][a-zA-Z0-9_\-./]*)\]\s*(.*)`)

func parseMemory(content, filename string) Memory {
	lines := strings.Split(content, "\n")
	var memory Memory
	base := strings.TrimSuffix(filename, ".md")
	memory.ID = strings.TrimPrefix(base, "memory_")

	inFrontmatter := false
	inTagsList := false
	var contentLines []string

	for _, line := range lines {
		if line == "---" {
			if !inFrontmatter {
				inFrontmatter = true
				continue
			}
			inFrontmatter = false
			continue
		}

		if inFrontmatter {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(line, "name:") {
				memory.Name = strings.TrimSpace(strings.TrimPrefix(line, "name:"))
				inTagsList = false
			} else if strings.HasPrefix(line, "description:") {
				memory.Description = strings.TrimSpace(strings.TrimPrefix(line, "description:"))
				inTagsList = false
			} else if strings.HasPrefix(line, "type:") {
				memory.Type = strings.TrimSpace(strings.TrimPrefix(line, "type:"))
				inTagsList = false
			} else if strings.HasPrefix(line, "project:") {
				memory.Project = strings.TrimSpace(strings.TrimPrefix(line, "project:"))
				inTagsList = false
			} else if strings.HasPrefix(line, "session:") {
				memory.Session = strings.TrimSpace(strings.TrimPrefix(line, "session:"))
				inTagsList = false
			} else if strings.HasPrefix(line, "file_path:") {
				memory.FilePath = strings.TrimSpace(strings.TrimPrefix(line, "file_path:"))
				inTagsList = false
			} else if strings.HasPrefix(line, "tags:") {
				value := strings.TrimSpace(strings.TrimPrefix(line, "tags:"))
				if value != "" {
					memory.Tags = parseTagsValue(value)
					inTagsList = false
				} else {
					memory.Tags = []string{}
					inTagsList = true
				}
			} else if strings.HasPrefix(line, "links:") {
				value := strings.TrimSpace(strings.TrimPrefix(line, "links:"))
				if value != "" {
					memory.Links = parseLinksValue(value)
				}
				inTagsList = false
			} else if inTagsList && strings.HasPrefix(trimmed, "- ") {
				tag := strings.TrimSpace(strings.TrimPrefix(trimmed, "- "))
				if tag != "" {
					memory.Tags = append(memory.Tags, tag)
				}
			} else if strings.HasPrefix(line, "created:") {
				value := strings.TrimSpace(strings.TrimPrefix(line, "created:"))
				if timestamp, err := time.Parse(time.RFC3339, value); err == nil {
					memory.Created = timestamp
				} else if unixSeconds, err := strconv.ParseInt(value, 10, 64); err == nil {
					memory.Created = time.Unix(unixSeconds, 0)
				}
				inTagsList = false
			} else if inTagsList && strings.Contains(line, ":") && !strings.HasPrefix(trimmed, "- ") {
				// A new key ended the YAML list.
				inTagsList = false
			}
			continue
		}

		contentLines = append(contentLines, line)
	}

	memory.Content = strings.Join(contentLines, "\n")
	memory.Sections = parseSections(memory.Content)
	return memory
}

// parseSections scans content for [slug] markers and returns addressable
// sections with line ranges (1-based, inclusive). Content without any [slug]
// markers gets a single implicit section with slug "body".
func parseSections(content string) []Section {
	lines := strings.Split(content, "\n")

	// Find slug line indices (0-based in lines slice).
	type slugPos struct {
		idx  int
		slug string
		rest string
	}
	var positions []slugPos
	for i, line := range lines {
		m := slugRe.FindStringSubmatch(line)
		if m != nil {
			positions = append(positions, slugPos{idx: i, slug: m[1], rest: m[2]})
		}
	}

	if len(positions) == 0 {
		// No slugs — one implicit section covering everything.
		preview := truncatePreview(strings.TrimSpace(content))
		return []Section{{
			Slug:      "body",
			Title:     preview,
			LineStart: 1,
			LineEnd:   len(lines),
			Preview:   preview,
		}}
	}

	var sections []Section
	for i, pos := range positions {
		start := pos.idx + 1 // 1-based
		var end int
		if i+1 < len(positions) {
			end = positions[i+1].idx // line before next slug (0-based idx = 1-based line - 1)
		} else {
			end = len(lines)
		}
		// Section content for preview/title.
		sectionLines := lines[pos.idx : end]
		sectionText := strings.TrimSpace(strings.Join(sectionLines, "\n"))
		title := strings.TrimSpace(pos.rest)
		if title == "" {
			// Use first non-empty line after the slug line as title.
			for _, l := range lines[pos.idx+1 : end] {
				t := strings.TrimSpace(l)
				if t != "" {
					title = t
					break
				}
			}
		}
		if title == "" {
			title = pos.slug
		}
		sections = append(sections, Section{
			Slug:      pos.slug,
			Title:     truncatePreview(title),
			LineStart: start,
			LineEnd:   end,
			Preview:   truncatePreview(sectionText),
		})
	}
	return sections
}

func truncatePreview(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 120 {
		return s[:120] + "..."
	}
	return s
}

func sanitizeYAMLValue(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\t", " ")
	return strings.Join(strings.Fields(s), " ")
}

func parseLinkItem(item string) Link {
	parts := strings.SplitN(item, ":", 3)
	if len(parts) < 2 {
		return Link{}
	}
	link := Link{
		Target:   strings.TrimSpace(parts[0]),
		Relation: strings.TrimSpace(parts[1]),
	}
	if len(parts) > 2 {
		link.Value = strings.TrimSpace(parts[2])
	}
	return link
}

func parseLinksValue(value string) []Link {
	var links []Link
	for _, item := range strings.Split(value, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		link := parseLinkItem(item)
		if link.Target != "" {
			links = append(links, link)
		}
	}
	return links
}

func formatLinksValue(memory Memory) string {
	var items []string
	for _, l := range memory.Links {
		item := l.Target + ":" + l.Relation
		if l.Value != "" {
			item += ":" + sanitizeYAMLValue(l.Value)
		}
		items = append(items, item)
	}
	return "links: " + strings.Join(items, ",")
}

func formatMemoryFile(memory Memory) string {
	tagsStr := strings.Join(memory.Tags, ",")
	sessionLine := ""
	if memory.Session != "" {
		sessionLine = fmt.Sprintf("session: %s\n", sanitizeYAMLValue(memory.Session))
	}
	projectLine := ""
	if memory.Project != "" {
		projectLine = fmt.Sprintf("project: %s\n", sanitizeYAMLValue(memory.Project))
	}
	filePathLine := ""
	if memory.FilePath != "" {
		filePathLine = fmt.Sprintf("file_path: %s\n", sanitizeYAMLValue(memory.FilePath))
	}
	tagsLine := ""
	if tagsStr != "" {
		tagsLine = fmt.Sprintf("tags: %s\n", tagsStr)
	}
	linksLine := ""
	if len(memory.Links) > 0 {
		linksLine = formatLinksValue(memory) + "\n"
	}

	return fmt.Sprintf(`---
name: %s
description: %s
type: %s
%s%s%s%s%screated: %s
---

%s
`, sanitizeYAMLValue(memory.Name), sanitizeYAMLValue(memory.Description), sanitizeYAMLValue(memory.Type), sessionLine, projectLine, filePathLine, tagsLine, linksLine, memory.Created.UTC().Format(time.RFC3339), memory.Content)
}

func findMemoryFileByID(memoryPath, memoryID string) (string, bool) {
	files, err := os.ReadDir(memoryPath)
	if err != nil {
		return "", false
	}

	for _, file := range files {
		if file.IsDir() || !strings.HasPrefix(file.Name(), "memory_") || !strings.HasSuffix(file.Name(), ".md") {
			continue
		}
		fileName := strings.TrimSuffix(file.Name(), ".md")
		if fileName == memoryID || fileName == "memory_"+memoryID || strings.HasSuffix(fileName, "_"+memoryID) {
			return filepath.Join(memoryPath, file.Name()), true
		}
	}
	return "", false
}

func updateSearchIndex(cfg *Config) {
	if cfg.GlobalConfig.AutoIndex {
		index, err := buildSearchIndex(cfg.MemoryDir)
		if err == nil {
			_ = saveSearchIndex(cfg.MemoryDir, index)
		}
	}
}
