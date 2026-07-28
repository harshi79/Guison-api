package syncer

import (
	"context"
	"crypto/sha256"
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
	"github.com/harshi79/project-17/internal/importer"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var copyColumns = []string{
	"source_id", "iin_start", "iin_end", "iin_length", "start8", "end8",
	"number_length", "luhn", "scheme", "brand", "card_type", "card_level", "prepaid",
	"country_alpha2", "country_alpha3", "country_name", "country_currency",
	"country_latitude", "country_longitude",
	"bank_name", "bank_url", "bank_phone", "bank_city", "bank_logo", "updated_at",
}

// Manager performs imports one at a time. Automatic checks and admin uploads use
// the same importer, so there is only one update path to understand.
type Manager struct {
	pool             *pgxpool.Pool
	github           *GitHubClient
	sources          []config.Source
	interval         time.Duration
	maxInvalidRatio  float64
	maxDownloadBytes int64
	mu               sync.Mutex
	wg               sync.WaitGroup
}

func New(pool *pgxpool.Pool, github *GitHubClient, sources []config.Source, interval time.Duration, maxInvalidRatio float64, maxDownloadBytes int64) *Manager {
	return &Manager{
		pool: pool, github: github, sources: sources, interval: interval,
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
	if err := m.pool.QueryRow(ctx, `SELECT current_sha FROM sources WHERE id=$1 AND enabled=true`, source.ID).Scan(&currentSHA); err != nil {
		return fmt.Errorf("read source state: %w", err)
	}
	if currentSHA == commit.SHA {
		_, err := m.pool.Exec(ctx, `UPDATE sources SET last_checked_at=now(), last_error='', updated_at=now() WHERE id=$1`, source.ID)
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
	_, err := m.pool.Exec(ctx, `
		INSERT INTO sources(id, repository, branch, path, parser, priority, enabled, status)
		VALUES($1,$2,$3,$4,$5,$6,true,'pending')
		ON CONFLICT(id) DO UPDATE SET
			repository=excluded.repository, branch=excluded.branch, path=excluded.path,
			parser=excluded.parser, priority=excluded.priority, enabled=true,
			status=CASE WHEN sources.record_count>0 THEN 'ready' ELSE 'pending' END,
			last_error='', updated_at=now()`,
		source.ID, source.Repository, source.Branch, source.Path, source.Format, source.Priority)
	return err
}

func (m *Manager) importFile(ctx context.Context, source config.Source, commit Commit, file *os.File, replace bool) (int64, error) {
	tx, err := m.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `CREATE TEMP TABLE staging_records (LIKE bin_records INCLUDING DEFAULTS EXCLUDING CONSTRAINTS EXCLUDING INDEXES) ON COMMIT DROP`); err != nil {
		return 0, fmt.Errorf("create staging table: %w", err)
	}
	parser, err := importer.NewCSVSource(file, source.ID, source.Format, commit.Date)
	if err != nil {
		return 0, err
	}
	if _, err := tx.CopyFrom(ctx, pgx.Identifier{"staging_records"}, copyColumns, parser); err != nil {
		return 0, fmt.Errorf("copy source data: %w", err)
	}
	if parser.TotalRows() > 0 && float64(parser.InvalidRows())/float64(parser.TotalRows()) > m.maxInvalidRatio {
		return 0, fmt.Errorf("%d of %d rows are invalid; maximum is %.2f%%",
			parser.InvalidRows(), parser.TotalRows(), m.maxInvalidRatio*100)
	}
	var stagedCount int64
	if err := tx.QueryRow(ctx, `
		SELECT count(*) FROM (
			SELECT 1 FROM staging_records GROUP BY source_id, iin_start, iin_end
		) AS unique_records`).Scan(&stagedCount); err != nil {
		return 0, fmt.Errorf("count staged records: %w", err)
	}
	if stagedCount < int64(source.MinRecords) {
		return 0, fmt.Errorf("import has %d unique valid records; at least %d required", stagedCount, source.MinRecords)
	}

	if replace {
		if _, err := tx.Exec(ctx, `DELETE FROM bin_records WHERE source_id=$1`, source.ID); err != nil {
			return 0, fmt.Errorf("remove old source records: %w", err)
		}
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO bin_records (`+strings.Join(copyColumns, ",")+`)
		SELECT DISTINCT ON (source_id, iin_start, iin_end) `+strings.Join(copyColumns, ",")+`
		FROM staging_records
		ORDER BY source_id, iin_start, iin_end, ctid DESC
		ON CONFLICT (source_id, iin_start, iin_end) DO UPDATE SET `+updateAssignments())
	if err != nil {
		return 0, fmt.Errorf("install source records: %w", err)
	}

	var recordCount int64
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM bin_records WHERE source_id=$1`, source.ID).Scan(&recordCount); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE sources SET current_sha=$2, status='ready', record_count=$3, skipped_rows=$4,
		       last_checked_at=now(), last_synced_at=now(), last_error='', updated_at=now()
		WHERE id=$1`, source.ID, commit.SHA, recordCount, parser.InvalidRows()); err != nil {
		return 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return recordCount, nil
}

func updateAssignments() string {
	assignments := make([]string, 0, len(copyColumns)-3)
	for _, column := range copyColumns[3:] {
		assignments = append(assignments, column+"=excluded."+column)
	}
	return strings.Join(assignments, ",")
}

func (m *Manager) recordFailure(ctx context.Context, sourceID string, importErr error) {
	failureCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	_, _ = m.pool.Exec(failureCtx, `
		UPDATE sources SET status=CASE WHEN record_count>0 THEN 'ready' ELSE 'error' END,
		last_checked_at=now(), last_error=$2, updated_at=now() WHERE id=$1`, sourceID, truncateError(importErr))
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
