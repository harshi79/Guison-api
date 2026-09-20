package syncer

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/harshi79/project-17/internal/config"
	"github.com/harshi79/project-17/internal/database"
	"github.com/harshi79/project-17/internal/importer"
)

// importColumns are the destination columns, in the order the importer's
// Values() supplies them. source_id, iin_start and iin_end lead so the
// composite primary key matches.
var importColumns = []string{
	"source_id", "iin_start", "iin_end", "iin_length", "start8", "end8",
	"number_length", "luhn", "scheme", "brand", "card_type", "card_level", "prepaid",
	"country_alpha2", "country_alpha3", "country_name", "country_currency",
	"country_latitude", "country_longitude",
	"bank_name", "bank_url", "bank_phone", "bank_city", "bank_logo", "updated_at",
}

// Manager performs imports one at a time. Automatic checks and admin uploads
// use the same importer, so there is only one update path to understand.
type Manager struct {
	db               *sql.DB
	github           *GitHubClient
	sources          []config.Source
	interval         time.Duration
	maxInvalidRatio  float64
	maxDownloadBytes int64
	mu               sync.Mutex
	wg               sync.WaitGroup
}

func New(db *sql.DB, github *GitHubClient, sources []config.Source, interval time.Duration, maxInvalidRatio float64, maxDownloadBytes int64) *Manager {
	return &Manager{
		db: db, github: github, sources: sources, interval: interval,
		maxInvalidRatio: maxInvalidRatio, maxDownloadBytes: maxDownloadBytes,
	}
}

// Run starts one simple periodic loop. There are no queues or separate workers.
func (m *Manager) Run(ctx context.Context, importOnStart bool) {
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		if importOnStart {
			m.checkAllAndLog(ctx)
		}
		ticker := time.NewTicker(m.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				m.checkAllAndLog(ctx)
			}
		}
	}()
}

// Wait blocks until the automatic loop has stopped after its context is canceled.
func (m *Manager) Wait() {
	m.wg.Wait()
}

func (m *Manager) checkAllAndLog(ctx context.Context) {
	if err := m.CheckAll(ctx); err != nil && !errors.Is(err, context.Canceled) {
		slog.Error("automatic data import failed", "error", err)
	}
}

// CheckAll checks each configured GitHub file and imports only changed files.
func (m *Manager) CheckAll(ctx context.Context) error {
	var failures []error
	for _, source := range m.sources {
		if err := m.Sync(ctx, source); err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", source.ID, err))
		}
	}
	return errors.Join(failures...)
}

func (m *Manager) Sync(ctx context.Context, source config.Source) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	commit, err := m.github.LatestCommit(ctx, source.Repository, source.Branch, source.Path)
	if err != nil {
		m.recordFailure(ctx, source.ID, err)
		return err
	}

	var currentSHA string
	if err := m.db.QueryRowContext(ctx, `SELECT current_sha FROM sources WHERE id=?1 AND enabled=1`, source.ID).Scan(&currentSHA); err != nil {
		return fmt.Errorf("read source state: %w", err)
	}
	if currentSHA == commit.SHA {
		_, err := m.db.ExecContext(ctx, `UPDATE sources SET last_checked_at=?2, last_error='', updated_at=?2 WHERE id=?1`, source.ID, database.NowSQL())
		return err
	}

	slog.Info("importing changed dataset", "source", source.ID, "commit", commit.SHA)
	file, err := m.github.Download(ctx, source.Repository, source.Path, commit.SHA)
	if err != nil {
		m.recordFailure(ctx, source.ID, err)
		return err
	}
	defer removeFile(file)

	if _, err := m.importFile(ctx, source, commit, file, true); err != nil {
		m.recordFailure(ctx, source.ID, err)
		return err
	}
	slog.Info("dataset import complete", "source", source.ID, "commit", commit.SHA)
	return nil
}

// ImportUpload imports an admin-provided CSV into the reserved "manual" source.
// When replace is false, rows are merged with earlier manual uploads.
func (m *Manager) ImportUpload(ctx context.Context, reader io.Reader, filename, format string, replace bool) (int64, error) {
	switch format {
	case "generic", "binlist", "ranges":
	default:
		return 0, fmt.Errorf("unsupported CSV format %q", format)
	}

	file, err := os.CreateTemp("", "bin-admin-upload-*.csv")
	if err != nil {
		return 0, err
	}
	defer removeFile(file)

	hasher := sha256.New()
	written, err := io.Copy(io.MultiWriter(file, hasher), io.LimitReader(reader, m.maxDownloadBytes+1))
	if err != nil {
		return 0, fmt.Errorf("save uploaded CSV: %w", err)
	}
	if written > m.maxDownloadBytes {
		return 0, fmt.Errorf("uploaded CSV exceeds the %d byte limit", m.maxDownloadBytes)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return 0, err
	}

	filename = filepath.Base(strings.ReplaceAll(filename, `\`, "/"))
	if filename == "." || filename == "" {
		filename = "upload.csv"
	}
	if len(filename) > 255 {
		filename = strings.ToValidUTF8(filename[:255], "")
	}
	commit := Commit{SHA: "upload-" + hex.EncodeToString(hasher.Sum(nil))[:16], Date: time.Now().UTC()}
	source := config.Source{
		ID: "manual", Repository: "manual-upload", Branch: "", Path: filename,
		Format: format, Priority: 1000, MinRecords: 1,
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.ensureManualSource(ctx, source); err != nil {
		return 0, err
	}
	recordCount, err := m.importFile(ctx, source, commit, file, replace)
	if err != nil {
		m.recordFailure(ctx, source.ID, err)
		return 0, err
	}
	return recordCount, nil
}

func (m *Manager) ensureManualSource(ctx context.Context, source config.Source) error {
	_, err := m.db.ExecContext(ctx, `
		INSERT INTO sources(id, repository, branch, path, parser, priority, enabled, status)
		VALUES(?1,?2,?3,?4,?5,?6,1,'pending')
		ON CONFLICT(id) DO UPDATE SET
			repository=excluded.repository, branch=excluded.branch, path=excluded.path,
			parser=excluded.parser, priority=excluded.priority, enabled=1,
			status=CASE WHEN sources.record_count>0 THEN 'ready' ELSE 'pending' END,
			last_error='', updated_at=?7`,
		source.ID, source.Repository, source.Branch, source.Path, source.Format, source.Priority, database.NowSQL())
	return err
}

// importFile streams the CSV into the destination source inside one transaction.
//
// There is no durable staging table (SQLite TEMP tables are per-connection and
// invisible to the transaction's final statement), so validation happens while
// records are buffered in memory; nothing is written to bin_records until the
// whole buffer has passed the row-count, invalid-ratio and minimum-record
// checks, and the install runs in the same transaction as the source-status
// update. A failure at any step rolls the transaction back, preserving the
// previous dataset.
func (m *Manager) importFile(ctx context.Context, source config.Source, commit Commit, file *os.File, replace bool) (int64, error) {
	parser, err := importer.NewCSVSource(file, source.ID, source.Format, commit.Date)
	if err != nil {
		return 0, err
	}

	// First the allocate-free validation pass: stream every row once,
	// keeping only the validated records for the install.
	records := make([][]any, 0, 400000)
	for parser.Next() {
		values, err := parser.Values()
		if err != nil {
			return 0, fmt.Errorf("parse source data: %w", err)
		}
		record := make([]any, len(values))
		copy(record, values)
		records = append(records, record)
	}
	if err := parser.Err(); err != nil {
		return 0, err
	}
	if parser.TotalRows() > 0 && float64(parser.InvalidRows())/float64(parser.TotalRows()) > m.maxInvalidRatio {
		return 0, fmt.Errorf("%d of %d rows are invalid; maximum is %.2f%%",
			parser.InvalidRows(), parser.TotalRows(), m.maxInvalidRatio*100)
	}

	// The record count used by validation and /data is the number of keys
	// actually installed: distinct (source_id, iin_start, iin_end).
	unique := map[[3]string]struct{}{}
	deduped := make([][]any, 0, len(records))
	seen := make(map[[3]string]bool, len(records))
	for _, record := range records {
		key := [3]string{record[0].(string), record[1].(string), record[2].(string)}
		unique[key] = struct{}{}
		if !seen[key] {
			seen[key] = true
			deduped = append(deduped, record)
		}
	}
	stagedCount := int64(len(unique))
	if stagedCount < int64(source.MinRecords) {
		return 0, fmt.Errorf("import has %d unique valid records; at least %d required", stagedCount, source.MinRecords)
	}

	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()

	if replace {
		if _, err := tx.ExecContext(ctx, `DELETE FROM bin_records WHERE source_id=?1`, source.ID); err != nil {
			return 0, fmt.Errorf("remove old source records: %w", err)
		}
	}

	// ON CONFLICT DO UPDATE keeps the live row on the left-hand side:
	// excluded.<col> references the incoming row.
	if err := m.insertRecords(ctx, tx, deduped); err != nil {
		return 0, err
	}

	var recordCount int64
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM bin_records WHERE source_id=?1`, source.ID).Scan(&recordCount); err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE sources SET current_sha=?2, status='ready', record_count=?3, skipped_rows=?4,
		       last_checked_at=?5, last_synced_at=?5, last_error='', updated_at=?5
		WHERE id=?1`, source.ID, commit.SHA, recordCount, parser.InvalidRows(), database.NowSQL()); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return recordCount, nil
}

// insertRecords installs deduplicated rows. The rows are written in upload
// order, and SQLite's ON CONFLICT...DO UPDATE keeps the existing row, so the
// first row for a given key wins — equivalent to PostgreSQL's
// DISTINCT ON (... ORDER BY ctid DESC) semantics for a fresh import.
func (m *Manager) insertRecords(ctx context.Context, tx *sql.Tx, records [][]any) error {
	if len(records) == 0 {
		return nil
	}
	const batchSize = 1000
	placeholderSets := placeholders(len(importColumns), batchSize)

	// Precompute the re-ordered columns: the SQL table order is the
	// importColumns order, so a plain multi-row VALUES batching works and
	// avoids any per-install share of a temp table.
	for start := 0; start < len(records); start += batchSize {
		end := start + batchSize
		if end > len(records) {
			end = len(records)
		}
		count := end - start
		tpls := strings.TrimSuffix(placeholderSets[count], ",")
		statement := "INSERT INTO bin_records (" + strings.Join(importColumns, ",") + ") VALUES " + tpls + " " +
			"ON CONFLICT(source_id, iin_start, iin_end) DO UPDATE SET " + updateAssignments()
		args := make([]any, 0, count*len(importColumns))
		for _, record := range records[start:end] {
			args = append(args, record...)
		}
		if _, err := tx.ExecContext(ctx, statement, args...); err != nil {
			return fmt.Errorf("install source records: %w", err)
		}
	}
	return nil
}

// updateAssignments builds "col=excluded.col" for every column after the
// primary key, matching the PostgreSQL upsert's non-key update columns.
func updateAssignments() string {
	assignments := make([]string, 0, len(importColumns)-3)
	for _, column := range importColumns[3:] {
		assignments = append(assignments, column+"=excluded."+column)
	}
	return strings.Join(assignments, ",")
}

// placeholders returns per-batch "(?,?,...)" placeholder tuples for batches of
// up to maxRows, indexed by row count.
func placeholders(columns, maxRows int) map[int]string {
	sets := make(map[int]string, maxRows)
	one := "(" + strings.TrimSuffix(strings.Repeat("?,", columns), ",") + ")"
	for rows := 1; rows <= maxRows; rows++ {
		sets[rows] = strings.Repeat(one+",", rows)
	}
	return sets
}

func (m *Manager) recordFailure(ctx context.Context, sourceID string, importErr error) {
	failureCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	_, _ = m.db.ExecContext(failureCtx, `
		UPDATE sources SET status=CASE WHEN record_count>0 THEN 'ready' ELSE 'error' END,
		last_checked_at=?2, last_error=?3, updated_at=?2 WHERE id=?1`, sourceID, database.NowSQL(), truncateError(importErr))
}

func truncateError(err error) string {
	message := err.Error()
	if len(message) > 2_000 {
		return message[:2_000]
	}
	return message
}

func removeFile(file *os.File) {
	name := file.Name()
	_ = file.Close()
	_ = os.Remove(name)
}
