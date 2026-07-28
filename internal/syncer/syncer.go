package syncer

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
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

type Purger interface{ Purge() }

type Manager struct {
	pool            *pgxpool.Pool
	github          *GitHubClient
	sources         []config.Source
	byID            map[string]config.Source
	interval        time.Duration
	maxInvalidRatio float64
	purger          Purger

	queue  chan string
	mu     sync.Mutex
	queued map[string]bool
}

func New(pool *pgxpool.Pool, github *GitHubClient, sources []config.Source, interval time.Duration, maxInvalidRatio float64, purger Purger) *Manager {
	byID := make(map[string]config.Source, len(sources))
	for _, source := range sources {
		byID[source.ID] = source
	}
	return &Manager{
		pool: pool, github: github, sources: sources, byID: byID,
		interval: interval, maxInvalidRatio: maxInvalidRatio, purger: purger,
		queue: make(chan string, len(sources)*2), queued: make(map[string]bool),
	}
}

func (m *Manager) Run(ctx context.Context, syncOnStart bool) {
	if syncOnStart {
		m.TriggerAll()
	}
	go m.worker(ctx)
	go func() {
		ticker := time.NewTicker(m.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				m.TriggerAll()
			}
		}
	}()
}

func (m *Manager) TriggerAll() {
	for _, source := range m.sources {
		m.Trigger(source.ID)
	}
}

func (m *Manager) Trigger(id string) bool {
	if _, ok := m.byID[id]; !ok {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.queued[id] {
		return true
	}
	select {
	case m.queue <- id:
		m.queued[id] = true
		return true
	default:
		return false
	}
}

func (m *Manager) SourcesForRepository(repository, ref string, changedPaths map[string]bool) []string {
	var ids []string
	for _, source := range m.sources {
		if source.Repository != repository || (ref != "" && ref != "refs/heads/"+source.Branch) {
			continue
		}
		if len(changedPaths) == 0 || changedPaths[source.Path] {
			ids = append(ids, source.ID)
		}
	}
	return ids
}

func (m *Manager) worker(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case id := <-m.queue:
			m.mu.Lock()
			delete(m.queued, id)
			m.mu.Unlock()
			source := m.byID[id]
			if err := m.Sync(ctx, source, false); err != nil && !errors.Is(err, context.Canceled) {
				slog.Error("source sync failed", "source", id, "error", err)
			}
		}
	}
}

func (m *Manager) Sync(ctx context.Context, source config.Source, force bool) error {
	commit, err := m.github.LatestCommit(ctx, source.Repository, source.Branch, source.Path)
	if err != nil {
		m.recordFailure(ctx, source.ID, err)
		return err
	}

	var currentSHA string
	if err := m.pool.QueryRow(ctx, `SELECT current_sha FROM sources WHERE id=$1 AND enabled=true`, source.ID).Scan(&currentSHA); err != nil {
		return fmt.Errorf("read source state: %w", err)
	}
	if !force && currentSHA == commit.SHA {
		_, err := m.pool.Exec(ctx, `UPDATE sources SET last_checked_at=now(), last_error='', status='ready', updated_at=now() WHERE id=$1`, source.ID)
		return err
	}

	slog.Info("downloading changed source", "source", source.ID, "commit", commit.SHA)
	file, err := m.github.Download(ctx, source.Repository, source.Path, commit.SHA)
	if err != nil {
		m.recordFailure(ctx, source.ID, err)
		return err
	}
	defer func() {
		name := file.Name()
		_ = file.Close()
		_ = os.Remove(name)
	}()

	if err := m.importFile(ctx, source, commit, file); err != nil {
		m.recordFailure(ctx, source.ID, err)
		return err
	}
	if m.purger != nil {
		m.purger.Purge()
	}
	slog.Info("source sync complete", "source", source.ID, "commit", commit.SHA)
	return nil
}

func (m *Manager) importFile(ctx context.Context, source config.Source, commit Commit, file *os.File) error {
	tx, err := m.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var locked bool
	if err := tx.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock(hashtext($1))`, "open-bin-sync:"+source.ID).Scan(&locked); err != nil {
		return err
	}
	if !locked {
		return nil // Another replica is already importing this source.
	}
	var currentSHA string
	if err := tx.QueryRow(ctx, `SELECT current_sha FROM sources WHERE id=$1 FOR UPDATE`, source.ID).Scan(&currentSHA); err != nil {
		return err
	}
	if currentSHA == commit.SHA {
		return tx.Commit(ctx)
	}
	if _, err := tx.Exec(ctx, `UPDATE sources SET status='syncing', last_checked_at=now(), updated_at=now() WHERE id=$1`, source.ID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `CREATE TEMP TABLE staging_records (LIKE bin_records INCLUDING DEFAULTS EXCLUDING CONSTRAINTS EXCLUDING INDEXES) ON COMMIT DROP`); err != nil {
		return fmt.Errorf("create staging table: %w", err)
	}

	parser, err := importer.NewCSVSource(file, source.ID, source.Format, commit.Date)
	if err != nil {
		return err
	}
	copied, err := tx.CopyFrom(ctx, pgx.Identifier{"staging_records"}, copyColumns, parser)
	if err != nil {
		return fmt.Errorf("copy source data: %w", err)
	}
	if copied < int64(source.MinRecords) {
		return fmt.Errorf("quality gate rejected import: got %d valid records, require at least %d", copied, source.MinRecords)
	}
	if parser.TotalRows() > 0 && float64(parser.InvalidRows())/float64(parser.TotalRows()) > m.maxInvalidRatio {
		return fmt.Errorf("quality gate rejected import: %d of %d rows invalid (limit %.2f%%)",
			parser.InvalidRows(), parser.TotalRows(), m.maxInvalidRatio*100)
	}

	if _, err := tx.Exec(ctx, `DELETE FROM bin_records WHERE source_id=$1`, source.ID); err != nil {
		return fmt.Errorf("remove old source records: %w", err)
	}
	tag, err := tx.Exec(ctx, `
		INSERT INTO bin_records (`+joinColumns(copyColumns)+`)
		SELECT DISTINCT ON (source_id, iin_start, iin_end) `+joinColumns(copyColumns)+`
		FROM staging_records
		ORDER BY source_id, iin_start, iin_end, ctid DESC`)
	if err != nil {
		return fmt.Errorf("install source records: %w", err)
	}
	recordCount := tag.RowsAffected()
	if _, err := tx.Exec(ctx, `
		UPDATE sources SET current_sha=$2, status='ready', record_count=$3, skipped_rows=$4,
		       last_checked_at=now(), last_synced_at=now(), last_error='', updated_at=now()
		WHERE id=$1`, source.ID, commit.SHA, recordCount, parser.InvalidRows()); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `SELECT pg_notify('bin_data_changed', $1)`, source.ID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (m *Manager) recordFailure(ctx context.Context, sourceID string, syncErr error) {
	failureCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	_, _ = m.pool.Exec(failureCtx, `
		UPDATE sources SET status=CASE WHEN record_count>0 THEN 'ready' ELSE 'error' END,
		last_checked_at=now(), last_error=$2, updated_at=now() WHERE id=$1`, sourceID, truncateError(syncErr))
}

func truncateError(err error) string {
	message := err.Error()
	if len(message) > 2_000 {
		return message[:2_000]
	}
	return message
}

func joinColumns(columns []string) string {
	result := ""
	for i, column := range columns {
		if i > 0 {
			result += ","
		}
		result += column
	}
	return result
}
