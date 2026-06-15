package validate

import "testing"

func TestNormalizePhrase(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "trim and lowercase",
			input: "  Hello WORLD  ",
			want:  "hello world",
		},
		{
			name:  "collapse spaces",
			input: "mot1   mot2    mot3",
			want:  "mot1 mot2 mot3",
		},
		{
			name:  "strip trailing punctuation",
			input: "mot1, mot2! mot3?",
			want:  "mot1 mot2 mot3",
		},
		{
			name:  "unicode lowercase",
			input: "ÉVÉNEMENT ÇA",
			want:  "événement ça",
		},
		{
			name:  "empty",
			input: "   ",
			want:  "",
		},
		{
			name:  "fifteen words realistic",
			input: "  Le   Premier Mot Est Ici, Et Le Dernier Aussi!  ",
			want:  "le premier mot est ici et le dernier aussi",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NormalizePhrase(tt.input)
			if got != tt.want {
				t.Errorf("NormalizePhrase(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
