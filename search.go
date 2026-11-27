package main

import (
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

// eachMemory calls fn with every memory in dir, holding one at a time.
//
// Nothing is indexed on disk: search reads the files, so it cannot be out of
// date. An index took several times more memory than the text it covered.
func eachMemory(dir string, fn func(Memory)) error {
	files, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	// Left by older versions, which trust it even when the files have changed
	// since. Removing it makes a process still running one rebuild it.
	_ = os.Remove(filepath.Join(dir, "search_index.json"))

	for _, file := range files {
		name := file.Name()
		if file.IsDir() || !strings.HasPrefix(name, "memory_") || !strings.HasSuffix(name, ".md") {
			continue
		}
		content, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		if memory := parseMemory(string(content), name); memory.ID != "" {
			fn(memory)
		}
	}
	return nil
}

// loadMemories returns every memory in dir by ID.
func loadMemories(dir string) (map[string]Memory, error) {
	memories := make(map[string]Memory)
	err := eachMemory(dir, func(m Memory) { memories[m.ID] = m })
	if err != nil {
		return nil, err
	}
	return memories, nil
}

// loadMemoriesIf loads the memories when needed, and otherwise only checks
// that dir can be read.
func loadMemoriesIf(needed bool, dir string) (map[string]Memory, error) {
	if needed {
		return loadMemories(dir)
	}
	_, err := os.ReadDir(dir)
	return nil, err
}

// scoredTerms reports which terms of a memory the query can match or score.
func scoredTerms(q Query) (wanted func(term string) bool, any bool) {
	exact := make(map[string]bool)
	for _, terms := range append([][]string{q.Must, q.Exclude}, q.Phrases...) {
		for _, term := range terms {
			exact[term] = true
		}
	}
	wanted = func(term string) bool {
		if exact[term] {
			return true
		}
		for _, prefix := range q.Prefixes {
			if strings.HasPrefix(term, prefix) {
				return true
			}
		}
		return false
	}
	return wanted, len(exact)+len(q.Prefixes) > 0
}

// searchMemoryDir ranks the memories of dir against query, best first, and
// the newest first among equals.
//
// A first pass counts how many memories hold each term of the query. The
// second scores them one at a time and keeps only the matches.
func searchMemoryDir(dir, query string, opts SearchOptions) ([]SearchResult, error) {
	wanted, anyTerm := scoredTerms(parseQuery(query))
	docFreq := make(map[string]int)
	docCount := 0
	if anyTerm {
		err := eachMemory(dir, func(m Memory) {
			docCount++
			seen := make(map[string]bool)
			for _, t := range tokenize(searchableText(m)) {
				if !seen[t.Term] && wanted(t.Term) {
					seen[t.Term] = true
					docFreq[t.Term]++
				}
			}
		})
		if err != nil {
			return nil, err
		}
	}

	// strict holds memories matching the whole query; fallback those matching
	// part of it, which are returned only when nothing matches it all.
	strict := make([]SearchResult, 0)
	fallback := make([]SearchResult, 0)
	anyWhole := false
	keep := func(results []SearchResult, result SearchResult) []SearchResult {
		if opts.HideSuperseded && result.SupersededBy != "" {
			return results
		}
		results = append(results, result)
		if opts.Limit > 0 && len(results)/2 >= opts.Limit {
			rankResults(results)
			clear(results[opts.Limit:]) // lets their text be freed
			results = results[:opts.Limit]
		}
		return results
	}
	err := eachMemory(dir, func(m Memory) {
		one := &searchDoc{
			TermFreq:      make(map[string]int),
			TermPositions: make(map[string][]int),
			DocFreq:       docFreq,
			DocCount:      docCount,
			Memory:        m,
		}
		if anyTerm {
			for _, t := range tokenize(searchableText(m)) {
				if wanted(t.Term) {
					one.TermFreq[t.Term]++
					one.TermPositions[t.Term] = append(one.TermPositions[t.Term], t.Pos)
				}
			}
		}
		result, whole, ok := scoreMemory(one, query, opts)
		switch {
		case !ok:
		case whole:
			anyWhole, fallback = true, nil
			strict = keep(strict, result)
		case !anyWhole:
			fallback = keep(fallback, result)
		}
	})
	if err != nil {
		return nil, err
	}

	results := strict
	if !anyWhole {
		results = append(results, fallback...)
	}
	rankResults(results)
	if opts.Limit > 0 && len(results) > opts.Limit {
		results = results[:opts.Limit]
	}
	return results, nil
}

// rankResults orders the best first, and the newest first among equals.
func rankResults(results []SearchResult) {
	sort.Slice(results, func(i, j int) bool {
		if results[i].Score != results[j].Score {
			return results[i].Score > results[j].Score
		}
		return results[i].MemoryID > results[j].MemoryID
	})
}

func calculateTFIDF(index *searchDoc, term string) float64 {
	if index.DocCount == 0 {
		return 0
	}
	tf := float64(index.TermFreq[term])
	df := float64(index.DocFreq[term])
	idf := math.Log(float64(index.DocCount+1) / (df + 1))
	return tf * idf
}

func memoryHasTerm(index *searchDoc, term string) bool {
	return index.TermFreq[term] > 0
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

func phraseMatches(index *searchDoc, phrase []string) (bool, int) {
	if len(phrase) == 0 {
		return true, 0
	}
	starts := index.TermPositions[phrase[0]]
	if len(starts) == 0 {
		return false, 0
	}

	count := 0
positions:
	for _, start := range starts {
		for i, term := range phrase {
			positions := index.TermPositions[term]
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

func prefixMatchTerms(index *searchDoc, prefix string) []string {
	var matched []string
	for term := range index.TermFreq {
		if strings.HasPrefix(term, prefix) {
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

func tagTFIDF(index *searchDoc, term string, tags []string) float64 {
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

func termMatches(index *searchDoc, tagTerms map[string]int, term string, tagOnly bool) bool {
	if tagOnly {
		return tagTerms[term] > 0
	}
	return memoryHasTerm(index, term)
}

func prefixMatchForQuery(index *searchDoc, tags []string, tagTerms map[string]int, prefix string, tagOnly bool) []string {
	if tagOnly {
		return tagPrefixMatchTerms(tags, prefix)
	}
	return prefixMatchTerms(index, prefix)
}

func phraseMatchesForQuery(index *searchDoc, tags []string, tagTerms map[string]int, phrase []string, tagOnly bool) (bool, int) {
	if tagOnly {
		return tagPhraseMatches(tags, phrase)
	}
	return phraseMatches(index, phrase)
}

func termTFIDF(index *searchDoc, tags []string, tagTerms map[string]int, term string, tagOnly bool) float64 {
	if tagOnly {
		return tagTFIDF(index, term, tags)
	}
	return calculateTFIDF(index, term)
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
		Sections:   memory.Sections,
		FilePath:   memory.FilePath,

		Stale:        memory.Stale,
		SupersededBy: memory.SupersededBy,
	}
}

// scoreMemory scores the memory of index against query. ok reports a match,
// and whole whether it matched every part of the query.
func scoreMemory(index *searchDoc, query string, opts SearchOptions) (result SearchResult, whole, ok bool) {
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
	totalRequired := len(q.Must) + len(q.Phrases) + len(q.Prefixes)

	memory := index.Memory
	if !matchesFieldFilters(q.Fields, memory) || !memoryHasAllTags(memory.Tags, filterTags) {
		return result, false, false
	}

	tagTerms := tagTokenCounts(memory.Tags)
	textLower := strings.ToLower(searchableText(memory))
	joinedTagsLower := strings.ToLower(strings.Join(memory.Tags, " "))

	for _, term := range q.Exclude {
		if termMatches(index, tagTerms, term, opts.TagOnly) {
			return result, false, false
		}
	}

	mustMatched := 0
	for _, term := range q.Must {
		if termMatches(index, tagTerms, term, opts.TagOnly) {
			mustMatched++
		}
	}

	phraseMatched := 0
	phraseOccurrences := 0
	for _, phrase := range q.Phrases {
		matches, n := phraseMatchesForQuery(index, memory.Tags, tagTerms, phrase, opts.TagOnly)
		if matches {
			phraseMatched++
			phraseOccurrences += n
		}
	}

	prefixMatched := 0
	var prefixTerms []string
	for _, prefix := range q.Prefixes {
		terms := prefixMatchForQuery(index, memory.Tags, tagTerms, prefix, opts.TagOnly)
		if len(terms) > 0 {
			prefixMatched++
			prefixTerms = append(prefixTerms, terms...)
		}
	}

	mustOK := len(q.Must) == 0 || mustMatched == len(q.Must)
	phraseOK := phraseMatched == len(q.Phrases)
	prefixOK := prefixMatched == len(q.Prefixes)

	if mustOK && phraseOK && prefixOK {
		// A fixed order, so that a score does not vary in its last digit.
		sort.Strings(prefixTerms)
		score := 1.0
		for _, term := range append(append([]string{}, q.Must...), prefixTerms...) {
			tfidf := termTFIDF(index, memory.Tags, tagTerms, term, opts.TagOnly)
			score += weights.TFIDF * tfidf
			if tagTerms[term] > 0 {
				score += weights.Tag * tfidf
			}
		}
		score += weights.Phrase * float64(phraseOccurrences)

		exactIn := textLower
		if opts.TagOnly {
			exactIn = joinedTagsLower
		}
		if strings.Contains(exactIn, lowerQuery) {
			score += weights.Exact
		}

		score = applyScoreBoosts(score, memory, weights)
		return memorySearchResult(memory.ID, memory, score), true, true
	}

	matched := mustMatched + phraseMatched + prefixMatched
	if matched == 0 {
		return result, false, false
	}
	score := (float64(matched) / float64(totalRequired)) * 2.0
	score = applyScoreBoosts(score, memory, weights)
	return memorySearchResult(memory.ID, memory, score), false, true
}
