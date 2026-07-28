package importer

import (
	"strings"
	"testing"
	"time"
)

func TestBinlistCSV(t *testing.T) {
	input := "BIN,Brand,Type,Category,Issuer,IssuerPhone,IssuerUrl,isoCode2,isoCode3,CountryName\n" +
		"002102,VISA,CREDIT,PLATINUM,Example Bank,+1,https://example.test,US,USA,United States\n"
	source, err := NewCSVSource(strings.NewReader(input), "test", "binlist", time.Unix(1, 0))
	if err != nil {
		t.Fatal(err)
	}
	if !source.Next() {
		t.Fatalf("expected a row: %v", source.Err())
	}
	r := source.current
	if r.IINStart != "002102" || r.Start8 != 210200 || r.End8 != 210299 {
		t.Fatalf("unexpected normalized range: %+v", r)
	}
	if r.Scheme != "visa" || r.CardType != "credit" || r.CardLevel != "platinum" {
		t.Fatalf("unexpected card fields: %+v", r)
	}
	if r.BankName != "Example Bank" || r.BankPhone != "+1" || r.BankURL != "https://example.test" {
		t.Fatalf("unexpected bank fields: %+v", r)
	}
	if r.CountryAlpha2 != "US" || r.CountryAlpha3 != "USA" || r.CountryName != "United States" {
		t.Fatalf("unexpected country fields: %+v", r)
	}
}

func TestRangesCSV(t *testing.T) {
	input := "iin_start,iin_end,number_length,scheme,brand,type,prepaid,country,bank_name\n" +
		"222100,272999,16,MASTERCARD,WORLD,DEBIT,false,IT,Banca Sella\n"
	source, err := NewCSVSource(strings.NewReader(input), "ranges", "ranges", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if !source.Next() {
		t.Fatalf("expected a row: %v", source.Err())
	}
	r := source.current
	if r.Start8 != 22210000 || r.End8 != 27299999 || r.Brand != "WORLD" {
		t.Fatalf("unexpected record: %+v", r)
	}
	if r.Prepaid == nil || *r.Prepaid {
		t.Fatalf("expected prepaid=false: %+v", r.Prepaid)
	}
}

func TestCamelCaseRangeHeaders(t *testing.T) {
	input := "IINStart,IINEnd,Scheme\n1234567,1234567,VISA\n"
	source, err := NewCSVSource(strings.NewReader(input), "test", "generic", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if !source.Next() || source.current.Start8 != 12345670 || source.current.End8 != 12345679 {
		t.Fatalf("camel-case range headers were not parsed: err=%v record=%+v", source.Err(), source.current)
	}
}

func TestMalformedCommunityHeaderIsRecovered(t *testing.T) {
	input := "543924,brand,type,category,issuer,alpha_2\n543925,VISA,DEBIT,,A Bank,GB\n"
	source, err := NewCSVSource(strings.NewReader(input), "community", "binlist", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if !source.Next() || source.current.IINStart != "543925" {
		t.Fatalf("header recovery failed: err=%v record=%+v", source.Err(), source.current)
	}
}

func TestInvalidRowsAreSkipped(t *testing.T) {
	input := "bin,brand\nnot-a-bin,VISA\n123456,VISA\n"
	source, err := NewCSVSource(strings.NewReader(input), "test", "binlist", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if !source.Next() || source.current.IINStart != "123456" {
		t.Fatal("valid row was not returned")
	}
	if source.InvalidRows() != 1 || source.TotalRows() != 2 {
		t.Fatalf("unexpected counts: total=%d invalid=%d", source.TotalRows(), source.InvalidRows())
	}
}

func TestNormalizeRange(t *testing.T) {
	tests := []struct {
		start, end         string
		wantStart, wantEnd int64
	}{
		{"123456", "123456", 12345600, 12345699},
		{"1234567", "1234567", 12345670, 12345679},
		{"12345678", "12345678", 12345678, 12345678},
		{"001234", "001234", 123400, 123499},
		{"0", "0", 0, 9999999},
	}
	for _, test := range tests {
		start, end, err := NormalizeRange(test.start, test.end)
		if err != nil || start != test.wantStart || end != test.wantEnd {
			t.Errorf("NormalizeRange(%q,%q)=(%d,%d,%v), want (%d,%d,nil)", test.start, test.end, start, end, err, test.wantStart, test.wantEnd)
		}
	}
}
