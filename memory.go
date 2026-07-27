package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

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
	return memory
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
