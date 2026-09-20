package database

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/harshi79/project-17/internal/config"
)

// openTestDB opens a fresh, file-backed SQLite database through the same Open
// path used in production. The production libsql driver delegates its file://
// (local) mode to the sqlite3 driver registered in sqlite_test.go, so these
// tests run against real local SQLite without any network access.
func openTestDB(t *testing.T) *DB {
	t.Helper()
	dsn := "file:" + filepath.Join(t.TempDir(), "test.db")
	db, err := Open(context.Background(), dsn, "test-token")
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestMigrateFromEmptyDatabase(t *testing.T) {
	db := openTestDB(t)
	for _, table := range []string{"sources", "bin_records", "schema_migrations"} {
		var exists bool
		if err := db.sql.QueryRow(`SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name=?1)`, table).Scan(&exists); err != nil {
			t.Fatalf("check table %s: %v", table, err)
		}
		if !exists {
			t.Errorf("table %s was not created", table)
		}
	}
	// Running migrations again must be a no-op.
	if err := db.Migrate(context.Background()); err != nil {
		t.Fatalf("re-applying migrations: %v", err)
	}
}

func TestConfigureSourcesFromEmpty(t *testing.T) {
	db := openTestDB(t)
	sources := []config.Source{{
		ID: "auto", Repository: "owner/repo", Branch: "main", Path: "bins.csv",
		Format: "binlist", Priority: 100, MinRecords: 1,
	}}
	if err := db.ConfigureSources(context.Background(), sources); err != nil {
		t.Fatalf("configure sources: %v", err)
	}
	var status string
	if err := db.sql.QueryRow(`SELECT status FROM sources WHERE id='auto'`).Scan(&status); err != nil {
		t.Fatalf("read source: %v", err)
	}
	if status != "pending" {
		t.Errorf("status=%q, want pending", status)
	}
}

func TestReadyRequiresCompletedSource(t *testing.T) {
	db := openTestDB(t)
	sources := []config.Source{{
		ID: "auto", Repository: "owner/repo", Branch: "main", Path: "bins.csv",
		Format: "binlist", Priority: 100, MinRecords: 1,
	}}
	if err := db.ConfigureSources(context.Background(), sources); err != nil {
		t.Fatalf("configure sources: %v", err)
	}
	if err := db.Ready(context.Background()); err == nil {
		t.Fatal("Ready() succeeded before any import completed")
	}
	now := NowSQL()
	if _, err := db.sql.Exec(`UPDATE sources SET status='ready', record_count=10, last_synced_at=?2 WHERE id=?1`, "auto", now); err != nil {
		t.Fatalf("mark ready: %v", err)
	}
	if err := db.Ready(context.Background()); err != nil {
		t.Fatalf("Ready() failed after import: %v", err)
	}
}

func TestLookupMostSpecificRangeWins(t *testing.T) {
	db := openTestDB(t)
	now := NowSQL()
	if _, err := db.sql.Exec(`INSERT INTO sources(id, repository, status, record_count, current_sha) VALUES('auto','owner/repo','ready',3,'abc123')`); err != nil {
		t.Fatalf("insert source: %v", err)
	}
	// A six-digit range and a more specific seven-digit prefix fallback.
	rows := [][]any{
		{"auto", "457173", "457173", 6, int64(45717300), int64(45717399), nil, nil, "visa", "Visa/Dankort", "debit", "", nil, "DK", "DNK", "Denmark", "DKK", nil, nil, "Jyske Bank", "www.jyskebank.dk", "", "Silkeborg", "", now},
		{"auto", "45717360", "45717360", 8, int64(45717360), int64(45717360), int64(16), boolPtr(true), "visa", "Visa", "debit", "platinum", nil, "DK", "DNK", "Denmark", "DKK", 56.0, 10.0, "Narrow Bank", "", "", "", "", now},
	}
	for _, r := range rows {
		if _, err := db.sql.Exec(`INSERT INTO bin_records
			(source_id,iin_start,iin_end,iin_length,start8,end8,number_length,luhn,scheme,brand,card_type,card_level,prepaid,
			 country_alpha2,country_alpha3,country_name,country_currency,country_latitude,country_longitude,
			 bank_name,bank_url,bank_phone,bank_city,bank_logo,updated_at)
			VALUES(?1,?2,?3,?4,?5,?6,?7,?8,?9,?10,?11,?12,?13,?14,?15,?16,?17,?18,?19,?20,?21,?22,?23,?24,?25)`,
			r...); err != nil {
			t.Fatalf("insert bin record: %v", err)
		}
	}

	result, err := db.Lookup(context.Background(), "45717360")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if result == nil {
		t.Fatal("lookup returned nil")
	}
	if result.Match.Length != 8 {
		t.Errorf("match.length=%d, want 8 (most specific range)", result.Match.Length)
	}
	if result.Bank.Name != "Narrow Bank" {
		t.Errorf("bank.name=%q, want Narrow Bank", result.Bank.Name)
	}
	if result.Number.Length == nil || *result.Number.Length != 16 {
		t.Errorf("number.length=%v, want 16", result.Number.Length)
	}
	if result.Country.Emoji != "🇩🇰" {
		t.Errorf("country.emoji=%q, want 🇩🇰", result.Country.Emoji)
	}

	// A 6-digit query resolves to the six-digit record (fallback).
	six, err := db.Lookup(context.Background(), "457173")
	if err != nil || six == nil {
		t.Fatalf("six-digit lookup failed: %v", err)
	}
	if six.Match.Length != 6 {
		t.Errorf("six-digit match.length=%d, want 6", six.Match.Length)
	}

	// No record covers this prefix.
	missing, err := db.Lookup(context.Background(), "999999")
	if err != nil || missing != nil {
		t.Fatalf("expected not-found, got result=%+v err=%v", missing, err)
	}
}

func TestLookupSkipsDisabledOrNotReadySources(t *testing.T) {
	db := openTestDB(t)
	if _, err := db.sql.Exec(`INSERT INTO sources(id, repository, status, record_count, current_sha)
		VALUES('ready','owner/repo','ready',1,'a'),('disabled','owner/repo','ready',1,'b'),('pending','owner/repo','pending',1,'c')`); err != nil {
		t.Fatalf("insert sources: %v", err)
	}
	now := NowSQL()
	for _, id := range []string{"ready", "disabled", "pending"} {
		enabled := 1
		if id == "disabled" {
			enabled = 0
		}
		if _, err := db.sql.Exec(`UPDATE sources SET enabled=?2 WHERE id=?1`, id, enabled); err != nil {
			t.Fatalf("set enabled: %v", err)
		}
	}
	for _, id := range []string{"ready", "disabled", "pending"} {
		if _, err := db.sql.Exec(`INSERT INTO bin_records
			(source_id,iin_start,iin_end,iin_length,start8,end8,scheme,updated_at)
			VALUES(?1,'457173','457173',6,45717300,45717399,'visa',?2)`, id, now); err != nil {
			t.Fatalf("insert record: %v", err)
		}
	}
	result, err := db.Lookup(context.Background(), "457173")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if result == nil || result.Source.ID != "ready" {
		t.Fatalf("expected the enabled ready source to win, got %+v", result)
	}
}

func TestLookupMatchesTrueRangeRows(t *testing.T) {
	db := openTestDB(t)
	// A range row (iin_start <> iin_end) exercises the UNION ALL range branch.
	if _, err := db.sql.Exec(`INSERT INTO sources(id, repository, status, record_count, current_sha)
		VALUES('ranges','owner/ranges','ready',1,'r')`); err != nil {
		t.Fatalf("insert source: %v", err)
	}
	if _, err := db.sql.Exec(`INSERT INTO bin_records
		(source_id,iin_start,iin_end,iin_length,start8,end8,scheme,bank_name,updated_at)
		VALUES('ranges','400000','499999',6,40000000,49999999,'visa','Range Bank',?1)`, NowSQL()); err != nil {
		t.Fatalf("insert range record: %v", err)
	}

	// An 8-digit query inside the range resolves through the range branch.
	result, err := db.Lookup(context.Background(), "45717360")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if result == nil || result.Bank.Name != "Range Bank" {
		t.Fatalf("range row should cover 45717360, got %+v", result)
	}

	// Outside the range: no match.
	missing, err := db.Lookup(context.Background(), "500000")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if missing != nil {
		t.Fatalf("500000 should not match the 400000-499999 range, got %+v", missing)
	}
}

func TestLookupPrefersManualPriority(t *testing.T) {
	db := openTestDB(t)
	if _, err := db.sql.Exec(`INSERT INTO sources(id, repository, status, record_count, current_sha) VALUES('auto','owner/repo','ready',1,'a')`); err != nil {
		t.Fatalf("insert source: %v", err)
	}
	// A manual (priority 1000) row and an automatic (priority 100) row both
	// cover the BIN; the manual row must win.
	if _, err := db.sql.Exec(`INSERT INTO sources(id, repository, priority, status, record_count, current_sha)
		VALUES('manual','manual-upload',1000,'ready',1,'m')`); err != nil {
		t.Fatalf("insert manual source: %v", err)
	}
	if _, err := db.sql.Exec(`INSERT INTO bin_records (source_id,iin_start,iin_end,iin_length,start8,end8,scheme,bank_name,updated_at)
		VALUES('auto','457173','457173',6,45717300,45717399,'visa','Auto Bank',?1),
		      ('manual','457173','457173',6,45717300,45717399,'visa','Manual Bank',?1)`, NowSQL()); err != nil {
		t.Fatalf("insert rows: %v", err)
	}
	result, err := db.Lookup(context.Background(), "457173")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if result == nil || result.Bank.Name != "Manual Bank" {
		t.Fatalf("manual priority row should win, got %+v", result)
	}
}

func TestStatsAggregatesSources(t *testing.T) {
	db := openTestDB(t)
	if _, err := db.sql.Exec(`INSERT INTO sources(id, repository, priority, status, record_count, enabled)
		VALUES('a','owner/a',100,'ready',10,1),('b','owner/b',50,'ready',5,1),('off','owner/c',10,'ready',999,0)`); err != nil {
		t.Fatalf("insert sources: %v", err)
	}
	stats, err := db.Stats(context.Background())
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if stats.Records != 15 {
		t.Errorf("records=%d, want 15 (disabled source excluded)", stats.Records)
	}
	if len(stats.Sources) != 2 {
		t.Errorf("sources=%d, want 2", len(stats.Sources))
	}
	if stats.Sources[0].ID != "a" {
		t.Errorf("first source=%q, want highest priority first", stats.Sources[0].ID)
	}
}

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

func boolPtr(v bool) *bool { return &v }
