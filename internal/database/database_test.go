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

func TestSixSevenAndEightDigitContainment(t *testing.T) {
	recordStart, recordEnd := int64(12345600), int64(12345699)
	for _, iin := range []string{"123456", "1234567", "12345678"} {
		queryStart, queryEnd, err := NormalizeQuery(iin)
		if err != nil {
			t.Fatal(err)
		}
		if recordStart > queryStart || recordEnd < queryEnd {
			t.Fatalf("six-digit record does not cover query %s", iin)
		}
	}

	sevenStart, sevenEnd, err := NormalizeQuery("1234567")
	if err != nil {
		t.Fatal(err)
	}
	sixStart, sixEnd, err := NormalizeQuery("123456")
	if err != nil {
		t.Fatal(err)
	}
	if sevenStart <= sixStart && sevenEnd >= sixEnd {
		t.Fatal("seven-digit record must not satisfy a less-specific six-digit query")
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
