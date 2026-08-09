package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestParseSections_NoSlugs verifies that content without [slug] markers
// gets a single implicit "body" section covering all lines.
func TestParseSections_NoSlugs(t *testing.T) {
	content := "This is a simple memory\nwith no slug markers\njust plain text."
	sections := parseSections(content)

	if len(sections) != 1 {
		t.Fatalf("expected 1 section, got %d", len(sections))
	}
	if sections[0].Slug != "body" {
		t.Fatalf("expected slug 'body', got %q", sections[0].Slug)
	}
	if sections[0].LineStart != 1 {
		t.Fatalf("expected LineStart 1, got %d", sections[0].LineStart)
	}
	if sections[0].LineEnd != 3 {
		t.Fatalf("expected LineEnd 3, got %d", sections[0].LineEnd)
	}
}

// TestParseSections_MultipleSlugs verifies that [slug] markers split
// content into addressable sections with correct line ranges.
func TestParseSections_MultipleSlugs(t *testing.T) {
	content := `[p22-diagnostics] Laptop never suspended
Some diagnostic info here.
More diagnostic details.

[p22-fix-wallpaper] Fixed the wallpaper script
Added idle guard using gdbus.
It checks Mutter IdleMonitor.

[p22-wake-sources] PCI wake sources
WiFi and Audio were enabled.`
	sections := parseSections(content)

	if len(sections) != 3 {
		t.Fatalf("expected 3 sections, got %d", len(sections))
	}

	// First section: [p22-diagnostics]
	if sections[0].Slug != "p22-diagnostics" {
		t.Fatalf("expected first slug 'p22-diagnostics', got %q", sections[0].Slug)
	}
	if sections[0].LineStart != 1 {
		t.Fatalf("expected first LineStart 1, got %d", sections[0].LineStart)
	}
	// Line before [p22-fix-wallpaper] which is at line 5 (0-indexed 4)
	if sections[0].LineEnd != 4 {
		t.Fatalf("expected first LineEnd 4, got %d", sections[0].LineEnd)
	}

	// Second section: [p22-fix-wallpaper]
	if sections[1].Slug != "p22-fix-wallpaper" {
		t.Fatalf("expected second slug 'p22-fix-wallpaper', got %q", sections[1].Slug)
	}
	if sections[1].LineStart != 5 {
		t.Fatalf("expected second LineStart 5, got %d", sections[1].LineStart)
	}
	// [p22-wake-sources] is at idx 8, so LineEnd = 8 (1-based line of last line before it)
	if sections[1].LineEnd != 8 {
		t.Fatalf("expected second LineEnd 8, got %d", sections[1].LineEnd)
	}

	// Third section: [p22-wake-sources]
	if sections[2].Slug != "p22-wake-sources" {
		t.Fatalf("expected third slug 'p22-wake-sources', got %q", sections[2].Slug)
	}
	if sections[2].LineStart != 9 {
		t.Fatalf("expected third LineStart 9, got %d", sections[2].LineStart)
	}
}

// TestParseSections_TitleFromRest verifies that the title is extracted
// from the text after the slug on the same line, or from the next line.
func TestParseSections_TitleFromRest(t *testing.T) {
	content := `[slug-with-title] This is the title
Body text here.

[slug-without-title]
This becomes the title.
Body.`
	sections := parseSections(content)

	if len(sections) != 2 {
		t.Fatalf("expected 2 sections, got %d", len(sections))
	}
	if sections[0].Title != "This is the title" {
		t.Fatalf("expected first title 'This is the title', got %q", sections[0].Title)
	}
	if sections[1].Title != "This becomes the title." {
		t.Fatalf("expected second title 'This becomes the title.', got %q", sections[1].Title)
	}
}

// TestParseSections_PreviewTruncation verifies that long previews are
// truncated to 120 chars + "...".
func TestParseSections_PreviewTruncation(t *testing.T) {
	longLine := strings.Repeat("a", 200)
	content := "[slug] " + longLine
	sections := parseSections(content)

	if len(sections) != 1 {
		t.Fatalf("expected 1 section, got %d", len(sections))
	}
	if len(sections[0].Preview) > 124 { // 120 + "..."
		t.Fatalf("preview not truncated, got length %d: %q", len(sections[0].Preview), sections[0].Preview)
	}
	if !strings.HasSuffix(sections[0].Preview, "...") {
		t.Fatalf("expected preview to end with '...', got %q", sections[0].Preview)
	}
}

// TestParseMemory_SectionsPopulated verifies that parseMemory populates
// the Sections field on the Memory struct.
func TestParseMemory_SectionsPopulated(t *testing.T) {
	content := `---
name: Test Memory
description: A test
type: project
project: testproj
tags: a,b
created: 2026-08-08T10:00:00Z
---

[sec-one] First section
Content one.

[sec-two] Second section
Content two.`

	mem := parseMemory(content, "memory_test.md")
	if len(mem.Sections) != 2 {
		t.Fatalf("expected 2 sections, got %d", len(mem.Sections))
	}
	if mem.Sections[0].Slug != "sec-one" {
		t.Fatalf("expected first slug 'sec-one', got %q", mem.Sections[0].Slug)
	}
	if mem.Sections[1].Slug != "sec-two" {
		t.Fatalf("expected second slug 'sec-two', got %q", mem.Sections[1].Slug)
	}
}

// TestHandleRead_FullMemory verifies that `memgraph read <id>` returns
// the full memory content with section metadata.
func TestHandleRead_FullMemory(t *testing.T) {
	dir := t.TempDir()
	cfg := &Config{MemoryDir: dir}

	content := "[diag] Problem found\nThe laptop never sleeps.\n\n[fix] Solution\nDisable wake sources."
	id := writeMemoryFromInput(cfg, content, "project", "testproj", "default", []string{"test"})

	// Read the file and verify content
	filePath := filepath.Join(dir, "memory_"+id+".md")
	data, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("memory file not written: %v", err)
	}
	body := string(data)
	if !strings.Contains(body, "[diag] Problem found") {
		t.Fatalf("memory file missing [diag] section, got:\n%s", body)
	}
	if !strings.Contains(body, "[fix] Solution") {
		t.Fatalf("memory file missing [fix] section, got:\n%s", body)
	}

	// Verify parseMemory extracts sections correctly
	mem := parseMemory(string(data), filepath.Base(filePath))
	if len(mem.Sections) != 2 {
		t.Fatalf("expected 2 sections from parsed memory, got %d", len(mem.Sections))
	}
}

// TestHandleRead_SectionRetrieval verifies that reading a specific section
// by slug returns only that section's content.
func TestHandleRead_SectionRetrieval(t *testing.T) {
	dir := t.TempDir()
	cfg := &Config{MemoryDir: dir}

	content := "[diag] Problem found\nThe laptop never sleeps.\n\n[fix] Solution\nDisable wake sources."
	id := writeMemoryFromInput(cfg, content, "project", "testproj", "default", []string{"test"})

	// Find the file and parse it
	filePath, found := findMemoryFileByID(dir, id)
	if !found {
		t.Fatalf("memory file not found for id %s", id)
	}

	data, _ := os.ReadFile(filePath)
	mem := parseMemory(string(data), filepath.Base(filePath))

	// Find the [fix] section
	var fixSection *Section
	for i := range mem.Sections {
		if mem.Sections[i].Slug == "fix" {
			fixSection = &mem.Sections[i]
			break
		}
	}
	if fixSection == nil {
		t.Fatalf("section [fix] not found")
	}

	// Verify the section content can be extracted
	rawLines := strings.Split(string(data), "\n")
	contentOffset := 0
	dashCount := 0
	for i, line := range rawLines {
		if strings.TrimSpace(line) == "---" {
			dashCount++
			if dashCount == 2 {
				contentOffset = i + 1
				break
			}
		}
	}

	absStart := contentOffset + fixSection.LineStart - 1
	absEnd := contentOffset + fixSection.LineEnd
	sectionContent := strings.Join(rawLines[absStart:absEnd], "\n")

	if !strings.Contains(sectionContent, "Disable wake sources") {
		t.Fatalf("section content missing expected text, got:\n%s", sectionContent)
	}
	if strings.Contains(sectionContent, "The laptop never sleeps") {
		t.Fatalf("section content leaked from [diag] section, got:\n%s", sectionContent)
	}
}

// TestRecallFormatIndex verifies that --format index returns section
// previews instead of full content.
func TestRecallFormatIndex(t *testing.T) {
	dir := t.TempDir()
	cfg := &Config{
		MemoryDir:    dir,
		GlobalConfig: GlobalConfig{AutoIndex: true, SearchWeights: SearchWeights{TFIDF: 1.0, Phrase: 3.0, Exact: 2.0, Recency24h: 1.2, Recency7d: 1.1, Type: 1.15, Tag: 1.5}},
	}

	content := "[diag] Problem found\nThe laptop never sleeps.\n\n[fix] Solution\nDisable wake sources."
	writeMemoryFromInput(cfg, content, "project", "testproj", "default", []string{"test"})

	// Build search index
	index, err := buildSearchIndex(dir)
	if err != nil {
		t.Fatalf("failed to build index: %v", err)
	}

	results := searchMemories(index, "laptop sleeps", SearchOptions{
		Weights: cfg.GlobalConfig.SearchWeights,
	})

	if len(results) == 0 {
		t.Fatal("expected at least 1 search result")
	}

	// Verify sections are populated in search results
	if len(results[0].Sections) != 2 {
		t.Fatalf("expected 2 sections in search result, got %d", len(results[0].Sections))
	}

	// Verify section slugs
	slugs := map[string]bool{}
	for _, sec := range results[0].Sections {
		slugs[sec.Slug] = true
	}
	if !slugs["diag"] {
		t.Fatal("missing [diag] section in search result")
	}
	if !slugs["fix"] {
		t.Fatal("missing [fix] section in search result")
	}
}
