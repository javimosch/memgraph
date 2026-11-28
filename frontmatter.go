package main

import "strings"

func isFence(line string) bool { return strings.TrimRight(line, " \t\r") == "---" }

// frontmatter finds the header of a memory file, given as lines: it opens with
// the first line that is not blank, if that is "---", and ends with the next
// "---". Every later "---" is part of the text. open is -1 and the text starts
// at line 0 when there is no complete header.
func frontmatter(lines []string) (open, body int) {
	for i, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if !isFence(line) {
			break
		}
		for j := i + 1; j < len(lines); j++ {
			if isFence(lines[j]) {
				return i, j + 1
			}
		}
		break
	}
	return -1, 0
}
