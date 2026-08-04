package main

import "strings"

// Plan heuristic and text extraction functions.
// Split from cmd_plans.go to keep files under 500 LOC.

// scorePlanLikelihood scores an .md file on how plan-like it is.
// Score >= 4 means it's probably a plan (requires strong structural signals).
//
// Signals:
//   - "## Phase" or "## Step" headings → +2
//   - "- [ ]" or "- [x]" checkboxes → +2
//   - Plan-like words in first 5 lines (plan, todo, task, roadmap, sprint) → +1
//
// Threshold of 4 means a file needs at least two strong signals (e.g. phases
// + checkboxes, or phases + plan words) to be detected. This prevents
// false positives on regular documentation.
func scorePlanLikelihood(text string) int {
	score := 0
	lines := strings.Split(text, "\n")

	// Check for phase/step headings
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "## Phase") ||
			strings.HasPrefix(trimmed, "## Step") ||
			strings.HasPrefix(trimmed, "## Sprint") ||
			strings.HasPrefix(trimmed, "## Milestone") {
			score += 2
			break
		}
	}

	// Check for checkboxes
	hasCheckbox := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "- [ ]") || strings.HasPrefix(trimmed, "- [x]") ||
			strings.HasPrefix(trimmed, "- [X]") {
			hasCheckbox = true
			break
		}
	}
	if hasCheckbox {
		score += 2
	}

	// Check first 5 lines for plan-like words
	firstLines := lines
	if len(firstLines) > 5 {
		firstLines = firstLines[:5]
	}
	header := strings.ToLower(strings.Join(firstLines, " "))
	planWords := []string{"plan", "todo", "task", "roadmap", "sprint", "backlog"}
	for _, w := range planWords {
		if strings.Contains(header, w) {
			score += 1
			break
		}
	}

	return score
}

// extractPlanName gets the first heading from a plan file.
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
