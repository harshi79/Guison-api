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

// importBatchSize caps how many parsed rows the import holds in memory at any
// moment. One batch of 1000 rows x 25 columns keeps peak heap at a few MB, so a
// source file of any size imports inside the memory budget of a small instance.
// Buffering the whole file instead peaked at ~580 MB of Go heap on the ~375K row
// upstream CSV, which a 512 MB instance OOM-killed mid-import (exit 137),
// restarting the container and the import in a loop.
const importBatchSize = 1000

// importProgressRows is how often a streaming import reports progress. Render
// shows only what the process prints, so a multi-minute import that stays
// silent is indistinguishable from a hung one.
const importProgressRows int64 = 50_000

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
	files, err := m.download(ctx, source, commit.SHA)
	if err != nil {
		m.recordFailure(ctx, source.ID, err)
		return err
	}
	defer removeFiles(files)

	if _, err := m.importFiles(ctx, source, commit, files, true); err != nil {
		m.recordFailure(ctx, source.ID, err)
		return err
	}
	slog.Info("dataset import complete", "source", source.ID, "commit", commit.SHA)
	return nil
}

// download fetches a source's data. A path naming a directory is a sharded
// source and every file in that directory is downloaded at the same commit, so
// the shards can be installed as one consistent snapshot.
func (m *Manager) download(ctx context.Context, source config.Source, sha string) ([]*os.File, error) {
	paths, isDirectory, err := m.github.ListDirectory(ctx, source.Repository, source.Path, sha)
	if err != nil {
		return nil, err
	}
	if !isDirectory {
		file, err := m.github.Download(ctx, source.Repository, source.Path, sha)
		if err != nil {
			return nil, err
		}
		return []*os.File{file}, nil
	}
	slog.Info("downloading sharded dataset", "source", source.ID, "files", len(paths))
	return m.github.DownloadAll(ctx, source.Repository, paths, sha)
}

// ImportUpload imports an admin-provided CSV into the reserved "manual" source.
// When replace is false, rows are merged with earlier manual uploads.
func (m *Manager) ImportUpload(ctx context.Context, reader io.Reader, filename, format string, replace bool) (int64, error) {
	switch format {
	case "generic", "binlist", "ranges", "openbiin":
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
	recordCount, err := m.importFiles(ctx, source, commit, []*os.File{file}, replace)
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

// importFile streams one CSV into the destination source inside one
// transaction. See importFiles.
func (m *Manager) importFile(ctx context.Context, source config.Source, commit Commit, file *os.File, replace bool) (int64, error) {
	return m.importFiles(ctx, source, commit, []*os.File{file}, replace)
}

// importFiles streams one or more CSVs into the destination source inside a
// single transaction.
//
// Each file is read row by row and installed in bounded batches
// (importBatchSize rows each) instead of being buffered whole, so peak memory is
// roughly one batch — a few MB — whatever the size or number of source files.
// Sharded sources (a directory of files) therefore cost no more memory than a
// single-file source: files are parsed one after another and share one batch,
// one set of parser counters and one transaction.
//
// There is no durable staging table (SQLite TEMP tables are per-connection and
// invisible to the transaction's final statement), so validation runs against
// the rows the transaction has just installed: the invalid-ratio check uses the
// parsers' own counters and the minimum-record check counts the source's rows
// back out of bin_records. Every write happens inside the single transaction,
// together with the source-status update, so a failure at any step — including
// a failure part-way through the last shard — rolls the transaction back and
// preserves the previously installed dataset.
func (m *Manager) importFiles(ctx context.Context, source config.Source, commit Commit, files []*os.File, replace bool) (int64, error) {
	if len(files) == 0 {
		return 0, errors.New("no source files to import")
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

	batch := newRecordBatch()
	statements := &insertStatements{}
	started := time.Now()
	var installed, totalRows, invalidRows, invalidRanges int64
	nextProgress := importProgressRows

	for _, file := range files {
		parser, err := importer.NewCSVSource(file, source.ID, source.Format, commit.Date)
		if err != nil {
			return 0, err
		}
		for parser.Next() {
			values, err := parser.Values()
			if err != nil {
				return 0, fmt.Errorf("parse source data: %w", err)
			}
			if err := batch.add(values); err != nil {
				return 0, err
			}
			if !batch.full() {
				continue
			}
			written, err := batch.flush(ctx, tx, statements)
			if err != nil {
				return 0, err
			}
			installed += written
			if installed >= nextProgress {
				slog.Info("dataset import in progress",
					"source", source.ID,
					"rows", installed,
					"skipped_rows", invalidRows+parser.InvalidRows(),
					"elapsed", time.Since(started).Round(time.Second).String())
				nextProgress += importProgressRows
			}
		}
		if err := parser.Err(); err != nil {
			return 0, err
		}
		totalRows += parser.TotalRows()
		invalidRows += parser.InvalidRows()
		invalidRanges += parser.InvalidRanges()
	}
	// The trailing partial batch, if any.
	written, err := batch.flush(ctx, tx, statements)
	if err != nil {
		return 0, err
	}
	installed += written

	if invalidRanges > 0 {
		slog.Warn("dataset rows contained unusable sub-ranges",
			"source", source.ID, "invalid_ranges", invalidRanges)
	}
	if totalRows > 0 && float64(invalidRows)/float64(totalRows) > m.maxInvalidRatio {
		return 0, fmt.Errorf("%d of %d rows are invalid; maximum is %.2f%%",
			invalidRows, totalRows, m.maxInvalidRatio*100)
	}

	// The record count used by validation and /data is the number of keys
	// actually installed: distinct (source_id, iin_start, iin_end).
	var recordCount int64
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM bin_records WHERE source_id=?1`, source.ID).Scan(&recordCount); err != nil {
		return 0, err
	}
	if recordCount < int64(source.MinRecords) {
		return 0, fmt.Errorf("import has %d unique valid records; at least %d required", recordCount, source.MinRecords)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE sources SET current_sha=?2, status='ready', record_count=?3, skipped_rows=?4,
		       last_checked_at=?5, last_synced_at=?5, last_error='', updated_at=?5
		WHERE id=?1`, source.ID, commit.SHA, recordCount, invalidRows, database.NowSQL()); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	slog.Info("dataset import installed",
		"source", source.ID, "rows", installed, "records", recordCount,
		"elapsed", time.Since(started).Round(time.Second).String())
	return recordCount, nil
}

// recordBatch accumulates at most importBatchSize parsed rows and installs them
// with a single multi-row upsert.
//
// Rows that repeat a primary key (source_id, iin_start, iin_end) collapse
// inside the batch and the LAST occurrence wins, matching the PostgreSQL
// import's SELECT DISTINCT ON (...) ... ORDER BY ctid DESC. Keys repeated
// across two batches resolve the same way: the upsert's DO UPDATE branch
// overwrites the earlier row with the later one, so the final state is always
// the last row the file contained for that key.
type recordBatch struct {
	rows  [][]any           // one importColumns-ordered row per distinct key
	index map[[3]string]int // primary key -> position in rows
}

func newRecordBatch() *recordBatch {
	return &recordBatch{
		rows:  make([][]any, 0, importBatchSize),
		index: make(map[[3]string]int, importBatchSize),
	}
}

// add buffers one parsed row. parser.Values() allocates a fresh slice for every
// row, so the batch keeps it as-is and never copies the whole file.
func (b *recordBatch) add(values []any) error {
	key, err := recordKey(values)
	if err != nil {
		return err
	}
	if position, duplicate := b.index[key]; duplicate {
		b.rows[position] = values // last occurrence wins
		return nil
	}
	b.index[key] = len(b.rows)
	b.rows = append(b.rows, values)
	return nil
}

// full reports whether the batch has room for no more rows.
func (b *recordBatch) full() bool { return len(b.rows) >= importBatchSize }

// flush installs the buffered rows with one statement and empties the batch for
// reuse, so an import of any length allocates no more than one batch of rows.
func (b *recordBatch) flush(ctx context.Context, tx *sql.Tx, statements *insertStatements) (int64, error) {
	if len(b.rows) == 0 {
		return 0, nil
	}
	// Args go in flat, row by row, in importColumns order.
	args := make([]any, 0, len(b.rows)*len(importColumns))
	for _, row := range b.rows {
		args = append(args, row...)
	}
	// ON CONFLICT DO UPDATE keeps the live row on the left-hand side:
	// excluded.<col> references the incoming row.
	if _, err := tx.ExecContext(ctx, statements.forRows(len(b.rows)), args...); err != nil {
		return 0, fmt.Errorf("install source records: %w", err)
	}
	installed := int64(len(b.rows))
	b.rows = b.rows[:0]
	clear(b.index)
	return installed, nil
}

// recordKey returns a row's primary key: the first three importColumns values
// (source_id, iin_start, iin_end), which the importer always supplies as text.
func recordKey(values []any) ([3]string, error) {
	var key [3]string
	if len(values) < len(importColumns) {
		return key, fmt.Errorf("parsed row has %d values; %d columns are required", len(values), len(importColumns))
	}
	for i := range key {
		text, ok := values[i].(string)
		if !ok {
			return key, fmt.Errorf("parsed column %s is not text", importColumns[i])
		}
		key[i] = text
	}
	return key, nil
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

// insertStatements caches the rendered upsert per row count. Every full batch of
// an import shares one statement and the trailing partial batch renders once, so
// a 375-batch import builds two SQL strings instead of 375. One import runs at a
// time (Manager.mu), so a per-import cache needs no locking.
type insertStatements struct {
	rows      int
	statement string
}

// forRows returns the multi-row upsert for a batch of the given size.
func (c *insertStatements) forRows(rows int) string {
	if c.rows != rows {
		c.statement = buildInsertStatement(rows)
		c.rows = rows
	}
	return c.statement
}

// buildInsertStatement renders one multi-row INSERT for rows rows of
// len(importColumns) columns, upserting on the primary key.
func buildInsertStatement(rows int) string {
	tuple := "(" + strings.TrimSuffix(strings.Repeat("?,", len(importColumns)), ",") + ")"
	var builder strings.Builder
	builder.Grow(512 + rows*(len(tuple)+1))
	builder.WriteString("INSERT INTO bin_records (")
	builder.WriteString(strings.Join(importColumns, ","))
	builder.WriteString(") VALUES ")
	for i := 0; i < rows; i++ {
		if i > 0 {
			builder.WriteByte(',')
		}
		builder.WriteString(tuple)
	}
	builder.WriteString(" ON CONFLICT(source_id, iin_start, iin_end) DO UPDATE SET ")
	builder.WriteString(updateAssignments())
	return builder.String()
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

func removeFiles(files []*os.File) {
	for _, file := range files {
		removeFile(file)
	}
}
