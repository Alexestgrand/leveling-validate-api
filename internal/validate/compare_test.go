package validate

import "testing"

func TestPhrasesMatch(t *testing.T) {
	secret := NormalizePhrase("Les Quinze Mots Secrets De La Phrase Finale Ici")

	tests := []struct {
		name      string
		submitted string
		want      bool
	}{
		{
			name:      "exact match after normalization",
			submitted: "les quinze mots secrets de la phrase finale ici",
			want:      true,
		},
		{
			name:      "match with extra spaces and punctuation",
			submitted: "  Les   quinze mots secrets, de la phrase finale ici!  ",
			want:      true,
		},
		{
			name:      "wrong phrase",
			submitted: "une phrase completement differente ici",
			want:      false,
		},
		{
			name:      "one word off",
			submitted: "les quinze mots secrets de la phrase finale la",
			want:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			normalized := NormalizePhrase(tt.submitted)
			got := PhrasesMatch(normalized, secret)
			if got != tt.want {
				t.Errorf("PhrasesMatch() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestValidateSubmission(t *testing.T) {
	if !ValidateSubmission("a valid phrase") {
		t.Error("expected valid submission")
	}
	if ValidateSubmission("") {
		t.Error("empty should be invalid")
	}
	if ValidateSubmission("   ") {
		t.Error("whitespace-only should be invalid")
	}
	long := make([]byte, MaxPhraseLength+1)
	for i := range long {
		long[i] = 'a'
	}
	if ValidateSubmission(string(long)) {
		t.Error("too long phrase should be invalid")
	}
}
