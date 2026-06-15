package validate

import (
	"strings"
)

var trailingPunct = map[rune]bool{
	'.': true,
	'!': true,
	'?': true,
	',': true,
	';': true,
	':': true,
}

// NormalizePhrase prepares a phrase for constant-time comparison.
func NormalizePhrase(phrase string) string {
	phrase = strings.TrimSpace(phrase)
	if phrase == "" {
		return ""
	}

	phrase = strings.ToLower(phrase)
	fields := strings.Fields(phrase)
	for i, word := range fields {
		fields[i] = trimTrailingPunct(word)
	}
	return strings.Join(fields, " ")
}

func trimTrailingPunct(word string) string {
	return strings.TrimRightFunc(word, func(r rune) bool {
		return trailingPunct[r]
	})
}
