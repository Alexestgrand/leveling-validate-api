package validate

import (
	"crypto/sha256"
	"crypto/subtle"
	"strings"
)

// PhrasesMatch compares two normalized phrases in constant time by hashing both
// with SHA-256 and using subtle.ConstantTimeCompare on the digests.
func PhrasesMatch(normalizedSubmitted, normalizedSecret string) bool {
	submittedHash := sha256.Sum256([]byte(normalizedSubmitted))
	secretHash := sha256.Sum256([]byte(normalizedSecret))
	return subtle.ConstantTimeCompare(submittedHash[:], secretHash[:]) == 1
}

// MaxPhraseLength is the maximum allowed length for a submitted phrase (characters).
const MaxPhraseLength = 1000

// ValidateSubmission checks whether a raw submitted phrase is acceptable for processing.
func ValidateSubmission(raw string) bool {
	raw = strings.TrimSpace(raw)
	return raw != "" && len(raw) <= MaxPhraseLength
}
