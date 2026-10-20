package main

import (
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"
)

// displayPath replaces whole-segment occurrences of the current user's home
// directory in text with "~" for terminal display.
func displayPath(text string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return text
	}
	return displayPathWithHome(text, home)
}

// displayPathWithHome replaces whole-segment occurrences of home in text with "~".
// When text equals home, it returns "~"; when a path starts with home followed
// by a path separator, it replaces that prefix with "~". Sibling directories
// sharing a string prefix (e.g. /home/developer when home is /home/dev) remain
// unchanged.
func displayPathWithHome(text, home string) string {
	home = strings.TrimSpace(home)
	if home == "" {
		return text
	}
	home = filepath.Clean(home)
	if home == "." || home == "/" || home == string(filepath.Separator) {
		return text
	}
	if !strings.Contains(text, home) {
		return text
	}

	var b strings.Builder
	b.Grow(len(text))

	from := 0
	for {
		idx := strings.Index(text[from:], home)
		if idx < 0 {
			b.WriteString(text[from:])
			break
		}
		start := from + idx
		end := start + len(home)

		beforeRune, _ := utf8.DecodeLastRuneInString(text[:start])
		afterRune, _ := utf8.DecodeRuneInString(text[end:])

		if isPathPrefixBoundary(beforeRune, start > 0) && isPathSegmentEnd(afterRune, end < len(text)) {
			b.WriteString(text[from:start])
			b.WriteString("~")
			from = end
			continue
		}

		b.WriteString(text[from : start+1])
		from = start + 1
	}

	return b.String()
}

func isPathPrefixBoundary(r rune, present bool) bool {
	if !present {
		return true
	}
	if r == '/' || r == '\\' || r == '_' || r == '-' || r == '.' || unicode.IsLetter(r) || unicode.IsDigit(r) {
		return false
	}
	return true
}

func isPathSegmentEnd(r rune, present bool) bool {
	if !present {
		return true
	}
	if r == '/' || r == rune(filepath.Separator) {
		return true
	}
	switch r {
	case ' ', '\t', '\n', '\r', '"', '\'', ')', ']', '}', ':', ';', ',':
		return true
	default:
		return false
	}
}
