package main

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

var stopWords = map[string]bool{
	"a": true, "an": true, "and": true, "are": true, "as": true, "at": true, "be": true, "by": true, "for": true, "from": true,
	"has": true, "have": true, "he": true, "in": true, "is": true, "it": true, "its": true, "of": true, "on": true, "that": true,
	"the": true, "to": true, "was": true, "were": true, "will": true, "with": true, "this": true, "but": true,
	"they": true, "or": true, "one": true, "had": true, "word": true, "not": true,
	"what": true, "all": true, "we": true, "when": true, "your": true, "can": true, "said": true, "there": true,
	"use": true, "each": true, "which": true, "she": true, "do": true, "how": true, "their": true, "if": true,
	"up": true, "other": true, "about": true, "out": true, "many": true, "then": true, "them": true, "these": true,
	"so": true, "some": true, "her": true, "would": true, "make": true, "like": true, "him": true, "into": true, "time": true,
	"look": true, "two": true, "more": true, "write": true, "go": true, "see": true, "number": true, "no": true,
	"way": true, "could": true, "people": true, "my": true, "than": true, "first": true, "been": true, "call": true,
	"who": true, "oil": true, "sit": true, "now": true, "find": true, "down": true, "day": true, "did": true, "get": true,
	"come": true, "made": true, "may": true, "part": true,
}

var tokenRe = regexp.MustCompile(`[a-zA-Z0-9]+`)

// Token represents a single indexed term and its position in a document.
type Token struct {
	Term string
	Pos  int
}

func tokenize(text string) []Token {
	matches := tokenRe.FindAllStringIndex(text, -1)
	tokens := make([]Token, 0, len(matches))
	pos := 0
	for _, m := range matches {
		term := strings.ToLower(text[m[0]:m[1]])
		if len(term) >= 2 {
			tokens = append(tokens, Token{Term: term, Pos: pos})
			pos++
		}
	}
	return tokens
}

func searchableText(memory Memory) string {
	return memory.Name + " " + memory.Description + " " + memory.Content + " " + memory.Project + " " + strings.Join(memory.Tags, " ")
}

func buildSearchIndex(memoryPath string) (*SearchIndex, error) {
	index := &SearchIndex{
		Version:       1,
		TermFreq:      make(map[string]map[string]int),
		TermPositions: make(map[string]map[string][]int),
		DocFreq:       make(map[string]int),
		Memories:      make(map[string]Memory),
	}

	files, err := os.ReadDir(memoryPath)
	if err != nil {
		return nil, err
	}

	for _, file := range files {
		if file.IsDir() || strings.HasPrefix(file.Name(), ".") {
			continue
		}
		if !strings.HasPrefix(file.Name(), "memory_") || !strings.HasSuffix(file.Name(), ".md") {
			continue
		}

		filePath := filepath.Join(memoryPath, file.Name())
		content, err := os.ReadFile(filePath)
		if err != nil {
			continue
		}

		memory := parseMemory(string(content), file.Name())
		if memory.ID == "" {
			continue
		}

		tokens := tokenize(searchableText(memory))
		seen := make(map[string]bool)
		for _, t := range tokens {
			if index.TermFreq[t.Term] == nil {
				index.TermFreq[t.Term] = make(map[string]int)
			}
			index.TermFreq[t.Term][memory.ID]++

			if index.TermPositions[t.Term] == nil {
				index.TermPositions[t.Term] = make(map[string][]int)
			}
			index.TermPositions[t.Term][memory.ID] = append(index.TermPositions[t.Term][memory.ID], t.Pos)

			if !seen[t.Term] {
				index.DocFreq[t.Term]++
				seen[t.Term] = true
			}
		}

		index.Memories[memory.ID] = memory
	}

	index.DocCount = len(index.Memories)
	return index, nil
}

func calculateTFIDF(index *SearchIndex, term string, memoryID string) float64 {
	if index.DocCount == 0 {
		return 0
	}
	tf := float64(index.TermFreq[term][memoryID])
	df := float64(index.DocFreq[term])
	idf := math.Log(float64(index.DocCount+1) / (df + 1))
	return tf * idf
}

func memoryHasTerm(index *SearchIndex, memoryID, term string) bool {
	return index.TermFreq[term][memoryID] > 0
}

func matchesFieldFilters(fields map[string]string, memory Memory) bool {
	for field, value := range fields {
		lowerValue := strings.ToLower(value)
		switch field {
		case "project":
			if !strings.EqualFold(value, memory.Project) {
				return false
			}
		case "type":
			if !strings.EqualFold(value, memory.Type) {
				return false
			}
		case "session":
			if !strings.EqualFold(value, memory.Session) {
				return false
			}
		case "tags":
			if !memoryHasAllTags(memory.Tags, parseTagsValue(value)) {
				return false
			}
		case "id":
			if !strings.Contains(strings.ToLower(memory.ID), lowerValue) {
				return false
			}
		default:
			// Unknown fields are ignored.
		}
	}
	return true
}

func phraseMatches(index *SearchIndex, memoryID string, phrase []string) (bool, int) {
	if len(phrase) == 0 {
		return true, 0
	}
	starts := index.TermPositions[phrase[0]][memoryID]
	if len(starts) == 0 {
		return false, 0
	}

	count := 0
positions:
	for _, start := range starts {
		for i, term := range phrase {
			positions := index.TermPositions[term][memoryID]
			if !containsInt(positions, start+i) {
				continue positions
			}
		}
		count++
	}
	return count > 0, count
}

func containsInt(values []int, target int) bool {
	for _, v := range values {
		if v == target {
			return true
		}
		if v > target {
			return false
		}
	}
	return false
}

func prefixMatchTerms(index *SearchIndex, memoryID, prefix string) []string {
	var matched []string
	for term := range index.TermFreq {
		if strings.HasPrefix(term, prefix) && index.TermFreq[term][memoryID] > 0 {
			matched = append(matched, term)
		}
	}
	return matched
}

func tagTokenCounts(tags []string) map[string]int {
	counts := make(map[string]int)
	for _, tag := range tags {
		for _, t := range tokenize(tag) {
			counts[t.Term]++
		}
	}
	return counts
}

func memoryHasAllTags(tags []string, required []string) bool {
	for _, r := range required {
		found := false
		for _, t := range tags {
			if strings.EqualFold(r, t) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func tagContainsTerm(tags []string, term string) bool {
	for _, tag := range tags {
		for _, t := range tokenize(tag) {
			if t.Term == term {
				return true
			}
		}
	}
	return false
}

func tagPrefixMatchTerms(tags []string, prefix string) []string {
	counts := tagTokenCounts(tags)
	var matched []string
	for term := range counts {
		if strings.HasPrefix(term, prefix) {
			matched = append(matched, term)
		}
	}
	return matched
}

func tagPhraseMatches(tags []string, phrase []string) (bool, int) {
	if len(phrase) == 0 {
		return true, 0
	}
	counts := tagTokenCounts(tags)
	min := 0
	for _, term := range phrase {
		c := counts[term]
		if c == 0 {
			return false, 0
		}
		if min == 0 || c < min {
			min = c
		}
	}
	return true, min
}

func tagTFIDF(index *SearchIndex, term string, tags []string) float64 {
	if index.DocCount == 0 {
		return 0
	}
	tf := float64(tagTokenCounts(tags)[term])
	df := float64(index.DocFreq[term])
	idf := math.Log(float64(index.DocCount+1) / (df + 1))
	return tf * idf
}

func applyScoreBoosts(score float64, memory Memory, weights SearchWeights) float64 {
	hoursSinceCreation := time.Since(memory.Created).Hours()
	if hoursSinceCreation < 24 {
		score *= weights.Recency24h
	} else if hoursSinceCreation < 168 {
		score *= weights.Recency7d
	}

	if memory.Type == "project" {
		score *= weights.Type
	}
	return score
}

func termMatches(index *SearchIndex, memoryID string, tagTerms map[string]int, term string, tagOnly bool) bool {
	if tagOnly {
		return tagTerms[term] > 0
	}
	return memoryHasTerm(index, memoryID, term)
}

func prefixMatchForQuery(index *SearchIndex, memoryID string, tags []string, tagTerms map[string]int, prefix string, tagOnly bool) []string {
	if tagOnly {
		return tagPrefixMatchTerms(tags, prefix)
	}
	return prefixMatchTerms(index, memoryID, prefix)
}

func phraseMatchesForQuery(index *SearchIndex, memoryID string, tags []string, tagTerms map[string]int, phrase []string, tagOnly bool) (bool, int) {
	if tagOnly {
		return tagPhraseMatches(tags, phrase)
	}
	return phraseMatches(index, memoryID, phrase)
}

func termTFIDF(index *SearchIndex, memoryID string, tags []string, tagTerms map[string]int, term string, tagOnly bool) float64 {
	if tagOnly {
		return tagTFIDF(index, term, tags)
	}
	return calculateTFIDF(index, term, memoryID)
}

func memorySearchResult(memoryID string, memory Memory, score float64) SearchResult {
	return SearchResult{
		MemoryID:   memoryID,
		Score:      score,
		Title:      memory.Name,
		Content:    memory.Content,
		MemoryType: memory.Type,
		Project:    memory.Project,
		Session:    memory.Session,
		Tags:       memory.Tags,
		Created:    memory.Created.Format(time.RFC3339),
	}
}

func searchMemories(index *SearchIndex, query string, opts SearchOptions) []SearchResult {
	q := parseQuery(query)
	if q.Fields == nil {
		q.Fields = make(map[string]string)
	}
	if opts.Project != "" {
		q.Fields["project"] = opts.Project
	}
	if opts.Session != "" {
		q.Fields["session"] = opts.Session
	}

	var filterTags []string
	if t, ok := q.Fields["tags"]; ok {
		filterTags = append(filterTags, parseTagsValue(t)...)
	}
	if len(opts.Tags) > 0 {
		filterTags = append(filterTags, opts.Tags...)
	}

	weights := opts.Weights
	lowerQuery := strings.ToLower(query)
	strict := make([]SearchResult, 0)
	fallback := make([]SearchResult, 0)
	totalRequired := len(q.Must) + len(q.Phrases) + len(q.Prefixes)

	for memoryID, memory := range index.Memories {
		if !matchesFieldFilters(q.Fields, memory) {
			continue
		}
		if !memoryHasAllTags(memory.Tags, filterTags) {
			continue
		}

		tagTerms := tagTokenCounts(memory.Tags)
		textLower := strings.ToLower(searchableText(memory))
		joinedTagsLower := strings.ToLower(strings.Join(memory.Tags, " "))

		excluded := false
		for _, term := range q.Exclude {
			if termMatches(index, memoryID, tagTerms, term, opts.TagOnly) {
				excluded = true
				break
			}
		}
		if excluded {
			continue
		}

		mustMatched := 0
		for _, term := range q.Must {
			if termMatches(index, memoryID, tagTerms, term, opts.TagOnly) {
				mustMatched++
			}
		}

		phraseMatched := 0
		phraseOccurrences := 0
		for _, phrase := range q.Phrases {
			ok, n := phraseMatchesForQuery(index, memoryID, memory.Tags, tagTerms, phrase, opts.TagOnly)
			if ok {
				phraseMatched++
				phraseOccurrences += n
			}
		}

		prefixMatched := 0
		var prefixTerms []string
		for _, prefix := range q.Prefixes {
			terms := prefixMatchForQuery(index, memoryID, memory.Tags, tagTerms, prefix, opts.TagOnly)
			if len(terms) > 0 {
				prefixMatched++
				prefixTerms = append(prefixTerms, terms...)
			}
		}

		mustOK := len(q.Must) == 0 || mustMatched == len(q.Must)
		phraseOK := phraseMatched == len(q.Phrases)
		prefixOK := prefixMatched == len(q.Prefixes)

		if mustOK && phraseOK && prefixOK {
			score := 1.0
			for _, term := range q.Must {
				tfidf := termTFIDF(index, memoryID, memory.Tags, tagTerms, term, opts.TagOnly)
				score += weights.TFIDF * tfidf
				if tagTerms[term] > 0 {
					score += weights.Tag * tfidf
				}
			}
			for _, term := range prefixTerms {
				tfidf := termTFIDF(index, memoryID, memory.Tags, tagTerms, term, opts.TagOnly)
				score += weights.TFIDF * tfidf
				if tagTerms[term] > 0 {
					score += weights.Tag * tfidf
				}
			}
			score += weights.Phrase * float64(phraseOccurrences)

			if opts.TagOnly {
				if strings.Contains(joinedTagsLower, lowerQuery) {
					score += weights.Exact
				}
			} else {
				if strings.Contains(textLower, lowerQuery) {
					score += weights.Exact
				}
			}

			score = applyScoreBoosts(score, memory, weights)
			strict = append(strict, memorySearchResult(memoryID, memory, score))
		} else if totalRequired > 0 {
			matched := mustMatched + phraseMatched + prefixMatched
			if matched > 0 {
				score := (float64(matched) / float64(totalRequired)) * 2.0
				score = applyScoreBoosts(score, memory, weights)
				fallback = append(fallback, memorySearchResult(memoryID, memory, score))
			}
		}
	}

	results := strict
	if len(results) == 0 {
		results = fallback
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})
	return results
}

func loadSearchIndex(memoryPath string) (*SearchIndex, error) {
	indexFile := filepath.Join(memoryPath, "search_index.json")
	if data, err := os.ReadFile(indexFile); err == nil {
		var index SearchIndex
		if err := json.Unmarshal(data, &index); err == nil && index.Version == 1 {
			return &index, nil
		}
	}

	index, err := buildSearchIndex(memoryPath)
	if err == nil {
		_ = saveSearchIndex(memoryPath, index)
	}
	return index, err
}

func saveSearchIndex(memoryPath string, index *SearchIndex) error {
	indexFile := filepath.Join(memoryPath, "search_index.json")
	data, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(indexFile, data, 0644)
}
