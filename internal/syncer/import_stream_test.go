package syncer

import (
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/harshi79/project-17/internal/config"
	"github.com/harshi79/project-17/internal/database"
	"github.com/harshi79/project-17/internal/importer"
)

// These tests cover the streaming import path: bounded batches, last-row-wins
// deduplication, the rendered upsert statement, the validation rules and the
// rollback that keeps a previously installed dataset intact when a new import
// is rejected.
//
// The database/sql handles come from the same database.Open used in
// production; the CGO sqlite3 driver registered in pipeline_e2e_test.go gives
// the tests a real local engine, so nothing here touches the network.

// testValues builds one row in importColumns order, the exact shape
// importer.Values() hands to the batch.
func testValues(sourceID, start, end, scheme string) []any {
	start8, end8, err := importer.NormalizeRange(start, end)
	if err != nil {
		panic(err)
	}
	return []any{
		sourceID, start, end, int16(len(start)), start8, end8,
		nil, nil, scheme, "test brand", "credit", "classic", nil,
		"US", "USA", "UNITED STATES", "USD",
		nil, nil,
		"TEST BANK", "https://example.test", "+10000000000", "TEST CITY", "logo.png",
		time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
	}
}

// writeCSV writes a synthetic binlist-format CSV. Each entry is a BIN and the
// value for its Brand column, which the binlist mapping reads as the scheme.
func writeCSV(t *testing.T, entries [][2]string) *os.File {
	t.Helper()
	file, err := os.Create(filepath.Join(t.TempDir(), "bins.csv"))
	if err != nil {
		t.Fatal(err)
	}
	writer := csv.NewWriter(file)
	if err := writer.Write([]string{"BIN", "Brand", "Type", "Category", "Issuer", "isoCode2", "isoCode3", "CountryName"}); err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if err := writer.Write([]string{entry[0], entry[1], "CREDIT", "STANDARD", "TEST BANK", "US", "USA", "UNITED STATES"}); err != nil {
			t.Fatal(err)
		}
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		t.Fatal(err)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = file.Close() })
	return file
}

func openTestDB(t *testing.T) *database.DB {
	t.Helper()
	dsn := "file:" + filepath.Join(t.TempDir(), "syncer-test.db")
	db, err := database.Open(context.Background(), dsn, "test-token")
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func testManager(db *database.DB, maxInvalidRatio float64) *Manager {
	return New(db.DB(), NewGitHubClient("", 100<<20), []config.Source{}, 0, maxInvalidRatio, 100<<20)
}

func TestRecordBatchKeepsLastDuplicate(t *testing.T) {
	batch := newRecordBatch()
	first := testValues("src", "400000", "400000", "first")
	other := testValues("src", "411111", "411111", "other")
	last := testValues("src", "400000", "400000", "last")
	for _, row := range [][]any{first, other, last} {
		if err := batch.add(row); err != nil {
			t.Fatalf("add row: %v", err)
		}
	}
	if len(batch.rows) != 2 {
		t.Fatalf("batch holds %d rows, want 2 distinct keys", len(batch.rows))
	}
	// The duplicate collapses onto the first-seen position but carries the
	// last occurrence's data (PostgreSQL's DISTINCT ON ... ORDER BY ctid DESC).
	if batch.rows[0][8] != "last" {
		t.Errorf("duplicate key kept scheme %v, want %q", batch.rows[0][8], "last")
	}
	if batch.rows[1][8] != "other" {
		t.Errorf("second key kept scheme %v, want %q", batch.rows[1][8], "other")
	}
	if len(batch.index) != 2 {
		t.Errorf("key index has %d entries, want 2", len(batch.index))
	}
}

func TestRecordBatchFullAtBatchSize(t *testing.T) {
	batch := newRecordBatch()
	for i := 0; i < importBatchSize-1; i++ {
		if err := batch.add(testValues("src", fmt.Sprintf("%06d", i), fmt.Sprintf("%06d", i), "visa")); err != nil {
			t.Fatalf("add row %d: %v", i, err)
		}
		if batch.full() {
			t.Fatalf("batch reported full after %d rows (limit %d)", i+1, importBatchSize)
		}
	}
	if err := batch.add(testValues("src", "999999", "999999", "visa")); err != nil {
		t.Fatalf("add final row: %v", err)
	}
	if !batch.full() {
		t.Fatalf("batch of %d rows is not full (limit %d)", len(batch.rows), importBatchSize)
	}
	if cap(batch.rows) != importBatchSize {
		t.Errorf("batch capacity is %d, want exactly one batch (%d)", cap(batch.rows), importBatchSize)
	}
}

func TestRecordBatchRejectsNonTextKey(t *testing.T) {
	batch := newRecordBatch()
	row := testValues("src", "400000", "400000", "visa")
	row[1] = 400000
	if err := batch.add(row); err == nil {
		t.Fatal("non-text iin_start was accepted")
	}
	short := testValues("src", "411111", "411111", "visa")[:10]
	if err := batch.add(short); err == nil {
		t.Fatal("short row was accepted")
	}
}

func TestBuildInsertStatement(t *testing.T) {
	const rows = 3
	statement := buildInsertStatement(rows)

	if !strings.HasPrefix(statement, "INSERT INTO bin_records ("+strings.Join(importColumns, ",")+") VALUES ") {
		t.Errorf("statement does not insert every column in order:\n%s", statement)
	}
	if want := rows * len(importColumns); strings.Count(statement, "?") != want {
		t.Errorf("statement has %d placeholders, want %d (%d rows x %d columns)",
			strings.Count(statement, "?"), want, rows, len(importColumns))
	}
	if !strings.Contains(statement, " ON CONFLICT(source_id, iin_start, iin_end) DO UPDATE SET ") {
		t.Errorf("statement is not an upsert on the primary key:\n%s", statement)
	}
	for _, column := range importColumns[3:] {
		if !strings.Contains(statement, column+"=excluded."+column) {
			t.Errorf("statement does not update column %q", column)
		}
	}
	if want := strings.Repeat("(?"+strings.Repeat(",?", len(importColumns)-1)+"),", rows); !strings.Contains(statement, strings.TrimSuffix(want, ",")) {
		t.Errorf("statement does not hold %d placeholder tuples:\n%s", rows, statement)
	}
}

func TestInsertStatementCacheReusesRenderedSQL(t *testing.T) {
	cache := &insertStatements{}
	full := cache.forRows(importBatchSize)
	if again := cache.forRows(importBatchSize); again != full {
		t.Error("cache re-rendered the statement for the same row count")
	}
	partial := cache.forRows(importBatchSize - 1)
	if partial == full {
		t.Error("cache returned the full-batch statement for a partial batch")
	}
	if strings.Count(partial, "?") != (importBatchSize-1)*len(importColumns) {
		t.Errorf("partial statement has %d placeholders, want %d",
			strings.Count(partial, "?"), (importBatchSize-1)*len(importColumns))
	}
}

// TestStreamingImportKeepsLastRow installs a file whose duplicates fall both
// inside one batch and across a batch boundary. Either way the last occurrence
// in the file is what ends up in the table.
func TestStreamingImportKeepsLastRow(t *testing.T) {
	db := openTestDB(t)
	entries := [][2]string{{"400000", "within-batch-first"}}
	for i := 1; i < importBatchSize; i++ {
		bin := fmt.Sprintf("%06d", 500000+i)
		entries = append(entries, [2]string{bin, "filler"})
	}
	// Row importBatchSize+1 repeats the first key, so the duplicate lands in
	// the second batch and is resolved by the upsert rather than the batch.
	entries = append(entries, [2]string{"400000", "across-batch-last"}, [2]string{"411111", "unique"})

	count, err := testManager(db, 0.05).ImportUpload(context.Background(), writeCSV(t, entries), "bins.csv", "binlist", true)
	if err != nil {
		t.Fatalf("import upload: %v", err)
	}
	if want := int64(importBatchSize + 1); count != want {
		t.Errorf("installed %d records, want %d distinct keys", count, want)
	}

	var scheme string
	if err := db.DB().QueryRow(`SELECT scheme FROM bin_records WHERE source_id='manual' AND iin_start='400000'`).Scan(&scheme); err != nil {
		t.Fatalf("read record: %v", err)
	}
	if scheme != "across-batch-last" {
		t.Errorf("duplicate key kept scheme %q, want the last row in the file", scheme)
	}
	var rows int64
	if err := db.DB().QueryRow(`SELECT count(*) FROM bin_records WHERE source_id='manual'`).Scan(&rows); err != nil {
		t.Fatalf("count records: %v", err)
	}
	if rows != count {
		t.Errorf("table holds %d rows, want %d", rows, count)
	}
}

// TestStreamingImportRejectsInvalidRatio keeps the pre-streaming validation: a
// file with too many unparsable rows installs nothing.
func TestStreamingImportRejectsInvalidRatio(t *testing.T) {
	db := openTestDB(t)
	entries := make([][2]string, 0, 20)
	for i := 0; i < 10; i++ {
		entries = append(entries, [2]string{fmt.Sprintf("%06d", 400000+i), "visa"})
	}
	for i := 0; i < 10; i++ {
		entries = append(entries, [2]string{"not-a-bin", "visa"})
	}

	_, err := testManager(db, 0.05).ImportUpload(context.Background(), writeCSV(t, entries), "bins.csv", "binlist", true)
	if err == nil {
		t.Fatal("import of a 50% invalid file succeeded")
	}
	if !strings.Contains(err.Error(), "rows are invalid") {
		t.Errorf("unexpected error: %v", err)
	}
	var rows int64
	if err := db.DB().QueryRow(`SELECT count(*) FROM bin_records WHERE source_id='manual'`).Scan(&rows); err != nil {
		t.Fatalf("count records: %v", err)
	}
	if rows != 0 {
		t.Errorf("rejected import left %d rows behind, want 0", rows)
	}
	var status string
	if err := db.DB().QueryRow(`SELECT status FROM sources WHERE id='manual'`).Scan(&status); err != nil {
		t.Fatalf("read source: %v", err)
	}
	if status == "ready" {
		t.Error("rejected import marked the source ready")
	}
}

// TestStreamingImportRollsBackBelowMinimumRecords proves the whole import runs
// in one transaction: a file that is too small is rejected and the previously
// installed dataset stays exactly as it was.
func TestStreamingImportRollsBackBelowMinimumRecords(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	source := config.Source{
		ID: "rollback-test", Repository: "owner/repo", Branch: "main", Path: "bins.csv",
		Format: "binlist", Priority: 100, MinRecords: 3,
	}
	if err := db.ConfigureSources(ctx, []config.Source{source}); err != nil {
		t.Fatalf("configure sources: %v", err)
	}
	manager := testManager(db, 0.05)
	commit := Commit{SHA: "abc123", Date: time.Now().UTC()}

	good := make([][2]string, 0, 5)
	for i := 0; i < 5; i++ {
		good = append(good, [2]string{fmt.Sprintf("%06d", 400000+i), "visa"})
	}
	installed, err := manager.importFile(ctx, source, commit, writeCSV(t, good), true)
	if err != nil {
		t.Fatalf("first import: %v", err)
	}
	if installed != 5 {
		t.Fatalf("first import installed %d records, want 5", installed)
	}

	tooSmall := source
	tooSmall.MinRecords = 100
	small := [][2]string{{"411111", "visa"}, {"422222", "visa"}}
	if _, err := manager.importFile(ctx, tooSmall, Commit{SHA: "def456", Date: time.Now().UTC()}, writeCSV(t, small), true); err == nil {
		t.Fatal("import below min_records succeeded")
	} else if !strings.Contains(err.Error(), "at least 100 required") {
		t.Errorf("unexpected error: %v", err)
	}

	var rows int64
	if err := db.DB().QueryRow(`SELECT count(*) FROM bin_records WHERE source_id=?1`, source.ID).Scan(&rows); err != nil {
		t.Fatalf("count records: %v", err)
	}
	if rows != 5 {
		t.Errorf("rollback left %d rows, want the 5 from the first import", rows)
	}
	var status, sha string
	var recordCount int64
	if err := db.DB().QueryRow(`SELECT status, current_sha, record_count FROM sources WHERE id=?1`, source.ID).Scan(&status, &sha, &recordCount); err != nil {
		t.Fatalf("read source: %v", err)
	}
	if status != "ready" || sha != commit.SHA || recordCount != 5 {
		t.Errorf("source state after rollback = %q/%q/%d, want ready/%s/5", status, sha, recordCount, commit.SHA)
	}
}

// TestStreamingImportMemoryBounded imports far more rows than a batch can hold
// and fails if the import retains them. The buffered implementation this
// replaced peaked at 584 MiB of Go heap on the real ~375K row file and was
// OOM-killed on a 512 MB instance; the streaming import stays near one batch
// (~5 MiB, measured). It also checks that progress is logged, since Render shows
// only what the process prints.
func TestStreamingImportMemoryBounded(t *testing.T) {
	if testing.Short() {
		t.Skip("long import")
	}
	const rows = 60_000
	const maxPeakBytes = 48 << 20

	dir := t.TempDir()
	path := filepath.Join(dir, "big.csv")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	writer := csv.NewWriter(file)
	if err := writer.Write([]string{"BIN", "Brand", "Type", "Category", "Issuer", "isoCode2", "isoCode3", "CountryName"}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < rows; i++ {
		// 8-digit BINs keep every row distinct and realistic in size.
		bin := fmt.Sprintf("%08d", 10000000+i)
		if err := writer.Write([]string{bin, "VISA", "CREDIT", "STANDARD", "SOME ISSUER BANK NAME", "US", "USA", "UNITED STATES"}); err != nil {
			t.Fatal(err)
		}
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	logs := &logCapture{}
	previous := slog.Default()
	slog.SetDefault(slog.New(logs))
	t.Cleanup(func() { slog.SetDefault(previous) })

	var (
		mu       sync.Mutex
		peak     uint64
		stop     = make(chan struct{})
		stopped  = make(chan struct{})
		memStats runtime.MemStats
	)
	go func() {
		defer close(stopped)
		ticker := time.NewTicker(2 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				runtime.ReadMemStats(&memStats)
				mu.Lock()
				if memStats.HeapAlloc > peak {
					peak = memStats.HeapAlloc
				}
				mu.Unlock()
			}
		}
	}()

	db := openTestDB(t)
	reader, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	count, err := testManager(db, 0.05).ImportUpload(context.Background(), reader, path, "binlist", true)
	close(stop)
	<-stopped

	if err != nil {
		t.Fatalf("import upload: %v", err)
	}
	if count != rows {
		t.Errorf("installed %d records, want %d", count, rows)
	}
	mu.Lock()
	peakBytes := peak
	mu.Unlock()
	t.Logf("peak HeapAlloc during import of %d rows: %.2f MiB", rows, float64(peakBytes)/(1<<20))
	if peakBytes > maxPeakBytes {
		t.Errorf("peak heap %.2f MiB exceeds the %d MiB budget; the import is buffering rows again",
			float64(peakBytes)/(1<<20), maxPeakBytes>>20)
	}

	entries := logs.lines()
	var progress, installed int
	for _, entry := range entries {
		switch {
		case strings.Contains(entry, "dataset import in progress"):
			progress++
		case strings.Contains(entry, "dataset import installed"):
			installed++
		}
	}
	if progress < rows/int(importProgressRows) {
		t.Errorf("logged %d progress lines for %d rows, want at least %d:\n%s",
			progress, rows, rows/int(importProgressRows), strings.Join(entries, "\n"))
	}
	if installed != 1 {
		t.Errorf("logged %d completion lines, want 1", installed)
	}
}

// logCapture is a minimal slog.Handler that records rendered messages so the
// test can assert on what Render would show.
type logCapture struct {
	mu      sync.Mutex
	entries []string
}

func (c *logCapture) Enabled(context.Context, slog.Level) bool { return true }

func (c *logCapture) Handle(_ context.Context, record slog.Record) error {
	var builder strings.Builder
	builder.WriteString(record.Message)
	record.Attrs(func(attribute slog.Attr) bool {
		fmt.Fprintf(&builder, " %s=%v", attribute.Key, attribute.Value)
		return true
	})
	c.mu.Lock()
	c.entries = append(c.entries, builder.String())
	c.mu.Unlock()
	return nil
}

func (c *logCapture) WithAttrs([]slog.Attr) slog.Handler { return c }
func (c *logCapture) WithGroup(string) slog.Handler      { return c }

func (c *logCapture) lines() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.entries...)
}
