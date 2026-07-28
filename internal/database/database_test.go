package database

import "testing"

func TestNormalizeQuery(t *testing.T) {
	tests := []struct {
		iin        string
		start, end int64
		valid      bool
	}{
		{"123456", 12345600, 12345699, true},
		{"00123456", 123456, 123456, true},
		{"1234567", 12345670, 12345679, true},
		{"12345", 0, 0, false},
		{"123456789", 0, 0, false},
		{"12345x", 0, 0, false},
	}
	for _, test := range tests {
		start, end, err := NormalizeQuery(test.iin)
		if test.valid && (err != nil || start != test.start || end != test.end) {
			t.Errorf("NormalizeQuery(%q)=(%d,%d,%v)", test.iin, start, end, err)
		}
		if !test.valid && err == nil {
			t.Errorf("NormalizeQuery(%q) unexpectedly succeeded", test.iin)
		}
	}
}

func TestCountryEmoji(t *testing.T) {
	if got := CountryEmoji("dk"); got != "🇩🇰" {
		t.Fatalf("got %q", got)
	}
	if got := CountryEmoji("unknown"); got != "" {
		t.Fatalf("got %q", got)
	}
}
