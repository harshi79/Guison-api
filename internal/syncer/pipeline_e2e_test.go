package syncer

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	_ "github.com/mattn/go-sqlite3"

	"github.com/harshi79/project-17/internal/config"
	"github.com/harshi79/project-17/internal/database"
)

// TestRealCsvSnapshotImport is a one-off integration check against a local
// snapshot of the real upstream CSV. It drives the exact production import
// path (Manager.ImportUpload, which builds and executes the multi-row
// ON CONFLICT install SQL) into a local file-backed SQLite database and
// verifies that approximately 375K records import and look up correctly. It
// is skipped when the fixture is not present.
func TestRealCsvSnapshotImport(t *testing.T) {
	dsn := "file:" + filepath.Join(t.TempDir(), "test.db")
	db, err := database.Open(context.Background(), dsn, "test-token")
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()

	candidates := []string{
		"/tmp/e2e/bin-list-data.csv",
		filepath.Join("..", "..", "e2e", "bin-list-data.csv"),
	}
	var path string
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			path = c
			break
		}
	}
	if path == "" {
		t.Skip("no CSV snapshot available")
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	manager := New(db.DB(), NewGitHubClient("", 100<<20), []config.Source{}, 0, 0.05, 100<<20)
	count, err := manager.ImportUpload(context.Background(), file, "bin-list-data.csv", "binlist", true)
	if err != nil {
		t.Fatalf("import upload: %v", err)
	}
	t.Logf("imported %d records", count)
	if count < 300000 || count > 400000 {
		t.Errorf("unexpected record count %d (expected ~374K)", count)
	}

	result, err := db.Lookup(context.Background(), "45717360")
	if err != nil || result == nil {
		t.Fatalf("lookup 45717360: %v (err=%v)", result, err)
	}
	t.Logf("45717360 -> scheme=%q bank=%q country=%q match.length=%d",
		result.Scheme, result.Bank.Name, result.Country.Name, result.Match.Length)

	six, err := db.Lookup(context.Background(), "457173")
	if err != nil || six == nil {
		t.Fatalf("lookup 457173 fallback: %v", err)
	}
	if six.Match.Length != 6 {
		t.Errorf("6-digit fallback length=%d, want 6", six.Match.Length)
	}
	if err := db.Ready(context.Background()); err != nil {
		t.Errorf("Ready() after import: %v", err)
	}

	// Verify source provenance is tracked.
	var status string
	var recordCount int64
	if err := db.DB().QueryRow(`SELECT status, record_count FROM sources WHERE id='manual'`).Scan(&status, &recordCount); err != nil && err != sql.ErrNoRows {
		t.Fatalf("read source: %v", err)
	}
	if recordCount != count {
		t.Errorf("sources.record_count=%d, want %d", recordCount, count)
	}
}
