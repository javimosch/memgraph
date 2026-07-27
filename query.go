package main

import "strings"

// Query represents a parsed search query.
type Query struct {
	Must     []string
	Exclude  []string
	Phrases  [][]string
	Prefixes []string
	Fields   map[string]string
}

func parseQuery(query string) Query {
	query = strings.TrimSpace(query)
	if query == "" {
		return Query{}
	}

	q := Query{Fields: make(map[string]string)}
	for _, tok := range splitQuery(query) {
		processQueryToken(tok, &q)
	}
	return q
}

func splitQuery(query string) []string {
	var tokens []string
	var buf strings.Builder
	inQuote := false

	for i := 0; i < len(query); i++ {
		c := query[i]
		if c == '"' {
			inQuote = !inQuote
			buf.WriteByte(c)
			continue
		}
		if (c == ' ' || c == '\t' || c == '\n' || c == '\r') && !inQuote {
			if buf.Len() > 0 {
				tokens = append(tokens, strings.TrimSpace(buf.String()))
				buf.Reset()
			}
			continue
		}
		buf.WriteByte(c)
	}

	if buf.Len() > 0 {
		tokens = append(tokens, strings.TrimSpace(buf.String()))
	}
	return tokens
}

func processQueryToken(tok string, q *Query) {
	tok = strings.TrimSpace(tok)
	if tok == "" {
		return
	}

	neg := false
	if strings.HasPrefix(tok, "-") {
		neg = true
		tok = strings.TrimPrefix(tok, "-")
	} else if strings.HasPrefix(tok, "+") {
		tok = strings.TrimPrefix(tok, "+")
	}
	if tok == "" {
		return
	}

	if key, value, ok := splitField(tok); ok && isSupportedField(key) {
		q.Fields[strings.ToLower(key)] = strings.Trim(value, "\"")
		return
	}

	if isQuoted(tok) {
		phrase := strings.Trim(tok, "\"")
		terms := phraseTerms(phrase)
		if len(terms) > 0 {
			q.Phrases = append(q.Phrases, terms)
		}
		return
	}

	if strings.HasSuffix(tok, "*") {
		prefix := strings.TrimSuffix(tok, "*")
		if len(prefix) >= 1 {
			if !neg {
				q.Prefixes = append(q.Prefixes, strings.ToLower(prefix))
			}
		}
		return
	}

	tokens := tokenize(tok)
	for _, t := range tokens {
		if neg {
			q.Exclude = append(q.Exclude, t.Term)
		} else if !stopWords[t.Term] {
			q.Must = append(q.Must, t.Term)
		}
	}
}

func splitField(tok string) (key, value string, ok bool) {
	idx := strings.Index(tok, ":")
	if idx <= 0 {
		return "", "", false
	}
	return tok[:idx], tok[idx+1:], true
}

func isSupportedField(key string) bool {
	switch strings.ToLower(key) {
	case "project", "tags", "type", "session", "id":
		return true
	}
	return false
}

func isQuoted(s string) bool {
	return len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"'
}

func phraseTerms(text string) []string {
	tokens := tokenize(text)
	terms := make([]string, len(tokens))
	for i, t := range tokens {
		terms[i] = t.Term
	}
	return terms
}
