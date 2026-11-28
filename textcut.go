package main

import "unicode/utf8"

// cutText returns at most the first n bytes of s, ending between characters:
// cutting inside one leaves invalid UTF-8, on screen or in a memory file.
func cutText(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}
