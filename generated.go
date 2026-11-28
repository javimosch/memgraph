package main

import (
	"os"
	"path/filepath"
	"regexp"
)

// generatedSession marks the memory files a skill graph build writes.
const generatedSession = "graph-from-dir"

// savedMemoryFile matches the files of saved memories, whose IDs are
// timestamps. A graph build never removes or replaces one, whatever it says.
var savedMemoryFile = regexp.MustCompile(`^memory_[0-9]{15,}(_[0-9]+)?\.md$`)

// isGeneratedFile reports whether a graph build wrote path, and so may remove
// or replace it. Anything it cannot tell is someone's file.
func isGeneratedFile(path string) bool {
	name := filepath.Base(path)
	if savedMemoryFile.MatchString(name) {
		return false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return parseMemory(string(data), name).Session == generatedSession
}
