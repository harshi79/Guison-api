package importer

import (
	"strings"
	"testing"
	"time"
)

// testTime is the fixed commit timestamp handed to parsers under test.
func testTime() time.Time { return time.Unix(1, 0).UTC() }

// collect drains a source and returns the records it yielded.
func collect(t *testing.T, source *CSVSource) []Record {
	t.Helper()
	var records []Record
	for source.Next() {
		value, err := source.Values()
		if err != nil {
			t.Fatalf("values: %v", err)
		}
		records = append(records, source.current)
		_ = value
	}
	if err := source.Err(); err != nil {
		t.Fatalf("read source: %v", err)
	}
	return records
}

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

// OpenBIIN files carry a fixed 6-digit BIN6 prefix plus a Ranges column holding
// the remaining PAN digits as pipe-separated contiguous blocks.
func TestOpenBIINCSVExpandsSubRanges(t *testing.T) {
	input := "BIN6,Ranges,Issuer,Country,Brand,Type\n" +
		"457100,40-45|51-53,NORDEA,DK,visa,debit\n"
	source, err := NewCSVSource(strings.NewReader(input), "openbiin", "openbiin", testTime())
	if err != nil {
		t.Fatalf("open openbiin csv: %v", err)
	}

	records := collect(t, source)
	if len(records) != 2 {
		t.Fatalf("row expanded to %d records, want 2", len(records))
	}
	if records[0].IINStart != "45710040" || records[0].IINEnd != "45710045" {
		t.Errorf("first block = %s..%s, want 45710040..45710045", records[0].IINStart, records[0].IINEnd)
	}
	if records[1].IINStart != "45710051" || records[1].IINEnd != "45710053" {
		t.Errorf("second block = %s..%s, want 45710051..45710053", records[1].IINStart, records[1].IINEnd)
	}
	// Upstream's Brand column is the payment network, matching how the binlist
	// format reads bin-list-data's Brand column.
	for i, record := range records {
		if record.Scheme != "visa" || record.CardType != "debit" ||
			record.BankName != "NORDEA" || record.CountryAlpha2 != "DK" {
			t.Errorf("record %d metadata = %q/%q/%q/%q, want visa/debit/NORDEA/DK",
				i, record.Scheme, record.CardType, record.BankName, record.CountryAlpha2)
		}
		if record.IINLength != 8 {
			t.Errorf("record %d IIN length = %d, want 8", i, record.IINLength)
		}
	}
	if records[0].Start8 != 45710040 || records[0].End8 != 45710045 {
		t.Errorf("first block 8-digit range = %d..%d", records[0].Start8, records[0].End8)
	}
}

func TestOpenBIINCSVSingleAndFullRange(t *testing.T) {
	input := "BIN6,Ranges,Issuer,Country,Brand,Type\n" +
		"457100,05,SINGLE BANK,US,visa,credit\n" +
		"457101,00-99,FULL BANK,US,visa,credit\n"
	source, err := NewCSVSource(strings.NewReader(input), "openbiin", "openbiin", testTime())
	if err != nil {
		t.Fatalf("open openbiin csv: %v", err)
	}
	records := collect(t, source)
	if len(records) != 2 {
		t.Fatalf("got %d records, want 2", len(records))
	}
	if records[0].IINStart != "45710005" || records[0].IINEnd != "45710005" {
		t.Errorf("single value block = %s..%s, want 45710005..45710005", records[0].IINStart, records[0].IINEnd)
	}
	if records[1].IINStart != "45710100" || records[1].IINEnd != "45710199" {
		t.Errorf("full block = %s..%s, want 45710100..45710199", records[1].IINStart, records[1].IINEnd)
	}
}

// Blocks the upstream rules forbid are dropped rather than guessed at, and a
// row that keeps no block at all is skipped entirely.
func TestOpenBIINCSVDropsUnusableBlocks(t *testing.T) {
	input := "BIN6,Ranges,Issuer,Country,Brand,Type\n" +
		"450090,00-10|60-10,BANK A,NG,visa,debit\n" +
		"450091,,BANK B,NG,visa,debit\n" +
		"450092,ab-cd,BANK C,NG,visa,debit\n" +
		"450093,00-99|5,BANK D,NG,visa,debit\n" +
		"457100,00-99|00-99,BANK E,DK,visa,debit\n"
	source, err := NewCSVSource(strings.NewReader(input), "openbiin", "openbiin", testTime())
	if err != nil {
		t.Fatalf("open openbiin csv: %v", err)
	}
	records := collect(t, source)

	// 450090 keeps 00-10 and loses the reversed 60-10; 450093 keeps 00-99 and
	// loses the 1-digit 5; 457100 keeps both of its identical blocks, which the
	// install batch then collapses onto one primary key.
	if len(records) != 4 {
		t.Fatalf("got %d records, want 4: %+v", len(records), records)
	}
	if records[0].IINStart != "45009000" || records[0].IINEnd != "45009010" {
		t.Errorf("first kept block = %s..%s", records[0].IINStart, records[0].IINEnd)
	}
	// InvalidRanges counts blocks lost from rows that were still imported: the
	// reversed 60-10 and the 1-digit 5. Blocks belonging to a row that was
	// skipped outright are already reported by InvalidRows, not counted twice.
	if source.InvalidRanges() != 2 {
		t.Errorf("dropped %d blocks, want 2", source.InvalidRanges())
	}
	// 450091 (blank) and 450092 (non-numeric) keep nothing and are skipped.
	if source.InvalidRows() != 2 {
		t.Errorf("skipped %d rows, want 2", source.InvalidRows())
	}
	if source.TotalRows() != 5 {
		t.Errorf("read %d rows, want 5", source.TotalRows())
	}
}

func TestOpenBIINCSVRequiresRangesColumn(t *testing.T) {
	// Without the Ranges column every row would import as a bare 6-digit BIN and
	// the sub-range precision would be lost silently, so this must fail loudly.
	input := "BIN6,Issuer,Country,Brand,Type\n457100,NORDEA,DK,visa,debit\n"
	if _, err := NewCSVSource(strings.NewReader(input), "openbiin", "openbiin", testTime()); err == nil {
		t.Fatal("openbiin CSV without a Ranges column was accepted")
	} else if !strings.Contains(err.Error(), "Ranges") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestOpenBIINCSVRejectsShortPrefix(t *testing.T) {
	input := "BIN6,Ranges,Issuer,Country,Brand,Type\n12345,00-99,BANK,US,visa,credit\n"
	source, err := NewCSVSource(strings.NewReader(input), "openbiin", "openbiin", testTime())
	if err != nil {
		t.Fatalf("open openbiin csv: %v", err)
	}
	if records := collect(t, source); len(records) != 0 {
		t.Fatalf("short BIN6 produced %d records, want 0", len(records))
	}
	if source.InvalidRows() != 1 {
		t.Errorf("skipped %d rows, want 1", source.InvalidRows())
	}
}

// Live OpenBIIN issuer names include values like `"ARMENIAN CARD" CJSC`: a field
// that opens with a quote and then continues after the closing quote. A plain
// encoding/csv reader treats that as an unterminated quoted field and swallows
// every following line until the next stray quote, which silently dropped 4,520
// real rows of the live 100-shard dataset. OpenBIIN records are one per line
// (upstream's rules forbid commas inside a column and its own reader splits on
// newlines), so the rows around such a name must all survive.
func TestOpenBIINCSVSurvivesStrayQuotesInIssuerNames(t *testing.T) {
	input := "BIN6,Ranges,Issuer,Country,Brand,Type\n" +
		"540294,00,\"ARMENIAN CARD\" CJSC,AM,mastercard,credit\n" +
		"540295,00-99,\"ARMENIAN CARD\" CJSC,AM,mastercard,credit\n" +
		"540296,05,PLAIN BANK,US,visa,debit\n" +
		"540297,00-99,JSC \"UNIVERSAL BANK\",UA,mastercard,debit\n" +
		"540298,00,RIVER BANK,US,visa,credit\n"
	source, err := NewCSVSource(strings.NewReader(input), "openbiin", "openbiin", testTime())
	if err != nil {
		t.Fatalf("open openbiin csv: %v", err)
	}
	records := collect(t, source)
	if len(records) != 5 {
		t.Fatalf("read %d records, want one per line (5)", len(records))
	}
	if source.TotalRows() != 5 || source.InvalidRows() != 0 {
		t.Errorf("rows=%d invalid=%d, want 5/0", source.TotalRows(), source.InvalidRows())
	}
	want := []string{"54029400", "54029500", "54029605", "54029700", "54029800"}
	for i, record := range records {
		if record.IINStart != want[i] {
			t.Errorf("record %d starts at %s, want %s", i, record.IINStart, want[i])
		}
	}
	if records[4].BankName != "RIVER BANK" || records[4].CountryAlpha2 != "US" {
		t.Errorf("row after a stray-quote name was corrupted: %+v", records[4])
	}
}
