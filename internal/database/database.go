package database

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/harshi79/project-17/internal/config"
	"github.com/harshi79/project-17/internal/model"
)

//go:embed migrations/*.sql
var migrations embed.FS

// DB wraps the SQLite/libSQL connection used for every query in the service.
// The application is otherwise stateless: all persistent data lives in the
// remote Turso database.
type DB struct {
	sql *sql.DB
}

// Open connects to the Turso (libSQL) database described by databaseURL and
// authToken, verifies connectivity, and applies pending schema migrations.
//
// The URL must use one of the schemes understood by the libSQL driver
// ("libsql://", "https://", "http://", "wss://", "ws://", or "file://" for
// local development/testing). No local SQLite file is required in production.
func Open(ctx context.Context, databaseURL, authToken string) (*DB, error) {
	connector, err := libsqlConnector(databaseURL, authToken)
	if err != nil {
		return nil, err
	}
	db := &DB{sql: sql.OpenDB(connector)}

	// A single in-flight connection is all this workload needs. The Turso
	// HTTP wire protocol shares one underlying HTTP client, so raising this
	// only drives more concurrent statements, not more sockets.
	db.sql.SetMaxOpenConns(4)
	db.sql.SetMaxIdleConns(1)
	db.sql.SetConnMaxIdleTime(30 * time.Minute)

	pingCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := db.sql.PingContext(pingCtx); err != nil {
		_ = db.sql.Close()
		return nil, fmt.Errorf("connect to database: %w", err)
	}
	if err := db.Migrate(ctx); err != nil {
		_ = db.sql.Close()
		return nil, err
	}
	return db, nil
}

// Close releases the underlying connection.
func (db *DB) Close() error { return db.sql.Close() }

// DB exposes the underlying database/sql handle for the syncer's transaction
// and bulk-import work.
func (db *DB) DB() *sql.DB { return db.sql }

// Migrate applies embedded schema migrations in lexicographic order, tracking
// which ones have already been applied in the schema_migrations table. Applied
// migrations are skipped, so this is safe to run on every startup and is
// idempotent for a database that already exists.
func (db *DB) Migrate(ctx context.Context) error {
	// The bookkeeping table must exist before we can query it.
	if _, err := db.sql.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (version TEXT PRIMARY KEY, applied_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')))`); err != nil {
		return fmt.Errorf("create migration table: %w", err)
	}
	entries, err := fs.ReadDir(migrations, "migrations")
	if err != nil {
		return err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		var applied bool
		err := db.sql.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=?1)`, entry.Name()).Scan(&applied)
		if err != nil {
			return err
		}
		if applied {
			continue
		}
		sqlText, err := migrations.ReadFile("migrations/" + entry.Name())
		if err != nil {
			return err
		}
		tx, err := db.sql.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err = tx.Exec(string(sqlText)); err == nil {
			_, err = tx.Exec(`INSERT INTO schema_migrations(version) VALUES(?1)`, entry.Name())
		}
		if err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("apply migration %s: %w", entry.Name(), err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %s: %w", entry.Name(), err)
		}
		slog.Info("database migration applied", "version", entry.Name())
	}
	return nil
}

// ConfigureSources registers the configured automatic sources and disables any
// previously-configured sources that no longer exist (the reserved "manual"
// source is managed from /data and must survive restarts).
func (db *DB) ConfigureSources(ctx context.Context, sources []config.Source) error {
	tx, err := db.sql.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.Exec(`UPDATE sources SET enabled=0, updated_at=?1 WHERE id <> 'manual'`, NowSQL()); err != nil {
		return err
	}
	for _, source := range sources {
		_, err := tx.Exec(`
			INSERT INTO sources(id, repository, branch, path, parser, priority, enabled)
			VALUES(?1,?2,?3,?4,?5,?6,1)
			ON CONFLICT(id) DO UPDATE SET
				current_sha=CASE WHEN sources.repository<>excluded.repository OR sources.branch<>excluded.branch OR sources.path<>excluded.path OR sources.parser<>excluded.parser THEN '' ELSE sources.current_sha END,
				status=CASE WHEN sources.repository<>excluded.repository OR sources.branch<>excluded.branch OR sources.path<>excluded.path OR sources.parser<>excluded.parser THEN 'pending' ELSE sources.status END,
				record_count=CASE WHEN sources.repository<>excluded.repository OR sources.branch<>excluded.branch OR sources.path<>excluded.path OR sources.parser<>excluded.parser THEN 0 ELSE sources.record_count END,
				skipped_rows=CASE WHEN sources.repository<>excluded.repository OR sources.branch<>excluded.branch OR sources.path<>excluded.path OR sources.parser<>excluded.parser THEN 0 ELSE sources.skipped_rows END,
				last_synced_at=CASE WHEN sources.repository<>excluded.repository OR sources.branch<>excluded.branch OR sources.path<>excluded.path OR sources.parser<>excluded.parser THEN NULL ELSE sources.last_synced_at END,
				repository=excluded.repository,
				branch=excluded.branch,
				path=excluded.path,
				parser=excluded.parser,
				priority=excluded.priority,
				enabled=1,
				last_error='',
				updated_at=?7`,
			source.ID, source.Repository, source.Branch, source.Path, source.Format, source.Priority, NowSQL())
		if err != nil {
			return fmt.Errorf("configure source %s: %w", source.ID, err)
		}
	}
	return tx.Commit()
}

// Lookup returns the most specific BIN record covering the supplied 6-8 digit
// IIN, or nil when no record covers it.
//
// Filtering order mirrors the original PostgreSQL query:
//  1. enabled, ready sources only,
//  2. (start8 <= qStart) AND (end8 >= qEnd) containment across the
//     NormalizeQuery-expanded range (8-digit space),
//  3. ORDER BY manual-first, then narrowest range, then the most specific
//     (longest) IIN length, then priority, then most recent update.
//
// SQLite has no GiST equivalent for a two-sided range-overlap search, so the
// single statement is rewritten as a two-branch UNION ALL that both plans
// answer with pure index seeks (no temp sort, no table scan):
//
//   - Single-BIN rows (iin_start = iin_end) are matched by the query's own
//     1..8 digit prefixes through bin_records_single_idx. A single row covers
//     exactly the 8-digit value spelled by its prefix, so prefix equality is
//     equivalent to containment and is a point lookup.
//   - True range rows (iin_start <> iin_end) use bin_records_range_idx with
//     the same start8 <= qStart AND end8 >= qEnd predicate as PostgreSQL.
//
// The UNION ALL sets are disjoint (a row is either single or a range), so the
// combined result is identical to the original query.
func (db *DB) Lookup(ctx context.Context, iin string) (*model.LookupResult, error) {
	start, end, err := NormalizeQuery(iin)
	if err != nil {
		return nil, err
	}
	var result model.LookupResult
	result.IIN = iin
	var (
		luhn, prepaid        sql.NullBool
		numberLength         sql.NullInt64
		latitude, longitude  sql.NullFloat64
		lastSyncedAt         sql.NullString
		sourceID, repository string
		currentSHA           string
	)
	prefixes := normalizePrefixes(iin)
	row := db.sql.QueryRowContext(ctx, lookupQuery, prefixes[0], prefixes[1], prefixes[2], prefixes[3], prefixes[4], prefixes[5], prefixes[6], prefixes[7], start, end)
	err = row.Scan(
		&result.Match.Start, &result.Match.End, &result.Match.Length,
		&numberLength, &luhn, &result.Scheme, &result.Brand, &result.Type, &result.Level, &prepaid,
		&result.Country.Alpha2, &result.Country.Alpha3, &result.Country.Name, &result.Country.Currency,
		&latitude, &longitude,
		&result.Bank.Name, &result.Bank.URL, &result.Bank.Phone, &result.Bank.City, &result.Bank.Logo,
		&sourceID, &repository, &currentSHA, &lastSyncedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("lookup iin: %w", err)
	}
	if numberLength.Valid {
		value := int16(numberLength.Int64)
		result.Number.Length = &value
	}
	if luhn.Valid {
		value := luhn.Bool
		result.Number.Luhn = &value
	}
	if prepaid.Valid {
		value := prepaid.Bool
		result.Prepaid = &value
	}
	if latitude.Valid {
		value := latitude.Float64
		result.Country.Latitude = &value
	}
	if longitude.Valid {
		value := longitude.Float64
		result.Country.Longitude = &value
	}
	result.Source.ID = sourceID
	result.Source.Repository = repository
	result.Source.Commit = currentSHA
	if lastSyncedAt.Valid {
		if parsed, parseErr := parseTime(lastSyncedAt.String); parseErr == nil {
			result.Source.SyncedAt = parsed
		}
	}
	result.Country.Emoji = CountryEmoji(result.Country.Alpha2)
	return &result, nil
}

// lookupQuery implements Lookup with two disjoint UNION ALL branches that
// SQLite can both answer via index seeks:
//
//   - single-BIN rows (iin_start = iin_end): exact-match the query's 1..8
//     digit prefixes through bin_records_single_idx;
//   - true range rows (iin_start <> iin_end): the original containment
//     predicate through bin_records_range_idx.
//
// Ordering columns are selected as sort_* aliases so the compound query can
// apply the original ORDER BY (manual-first, narrowest range, longest IIN,
// priority, most recent) across the union, then LIMIT 1.
const lookupQuery = `
SELECT iin_start, iin_end, iin_length,
       number_length, luhn, scheme, brand, card_type, card_level, prepaid,
       country_alpha2, country_alpha3, country_name, country_currency,
       country_latitude, country_longitude,
       bank_name, bank_url, bank_phone, bank_city, bank_logo,
       src_id, src_repository, src_sha, src_synced
FROM (
    SELECT b.iin_start AS iin_start, b.iin_end AS iin_end, b.iin_length AS iin_length,
           b.number_length AS number_length, b.luhn AS luhn, b.scheme AS scheme, b.brand AS brand,
           b.card_type AS card_type, b.card_level AS card_level, b.prepaid AS prepaid,
           b.country_alpha2 AS country_alpha2, b.country_alpha3 AS country_alpha3,
           b.country_name AS country_name, b.country_currency AS country_currency,
           b.country_latitude AS country_latitude, b.country_longitude AS country_longitude,
           b.bank_name AS bank_name, b.bank_url AS bank_url, b.bank_phone AS bank_phone,
           b.bank_city AS bank_city, b.bank_logo AS bank_logo,
           s.id AS src_id, s.repository AS src_repository, s.current_sha AS src_sha,
           s.last_synced_at AS src_synced,
           (s.priority = 1000) AS sort_manual, (b.end8 - b.start8) AS sort_width,
           b.iin_length AS sort_length, s.priority AS sort_priority, b.updated_at AS sort_updated
    FROM bin_records b
    JOIN sources s ON s.id = b.source_id AND s.enabled = 1 AND s.status = 'ready'
    WHERE b.iin_start = b.iin_end AND b.iin_start IN (?1,?2,?3,?4,?5,?6,?7,?8)
    UNION ALL
    SELECT b.iin_start, b.iin_end, b.iin_length,
           b.number_length, b.luhn, b.scheme, b.brand,
           b.card_type, b.card_level, b.prepaid,
           b.country_alpha2, b.country_alpha3, b.country_name, b.country_currency,
           b.country_latitude, b.country_longitude,
           b.bank_name, b.bank_url, b.bank_phone, b.bank_city, b.bank_logo,
           s.id, s.repository, s.current_sha, s.last_synced_at,
           (s.priority = 1000), (b.end8 - b.start8), b.iin_length, s.priority, b.updated_at
    FROM bin_records b
    JOIN sources s ON s.id = b.source_id AND s.enabled = 1 AND s.status = 'ready'
    WHERE b.iin_start <> b.iin_end AND b.start8 <= ?9 AND b.end8 >= ?10
)
ORDER BY sort_manual DESC, sort_width ASC, sort_length DESC, sort_priority DESC, sort_updated DESC
LIMIT 1`

// NormalizeQuery expands a 6-8 digit IIN into the inclusive integer range it
// covers in the 8-digit space, so it can be tested against start8/end8.
func NormalizeQuery(iin string) (int64, int64, error) {
	if len(iin) < 6 || len(iin) > 8 {
		return 0, 0, errors.New("IIN must contain 6 to 8 digits")
	}
	var value int64
	for _, r := range iin {
		if r < '0' || r > '9' {
			return 0, 0, errors.New("IIN must contain only digits")
		}
		value = value*10 + int64(r-'0')
	}
	scale := pow10(8 - len(iin))
	return value * scale, (value+1)*scale - 1, nil
}

// normalizePrefixes returns the eight candidate single-BIN prefixes for a
// lookup: every 1..len(iin)-digit prefix of the query string (the only
// lengths a single-row record can have and still contain the query), followed
// by the full query string for the remaining slots to make the IN list
// harmless. The caller has already validated the IIN.
func normalizePrefixes(iin string) [8]string {
	var prefixes [8]string
	for i := range prefixes {
		if i < len(iin) {
			prefixes[i] = iin[:i+1]
		} else {
			prefixes[i] = iin
		}
	}
	return prefixes
}

func CountryEmoji(alpha2 string) string {
	alpha2 = strings.ToUpper(alpha2)
	if len(alpha2) != 2 || alpha2[0] < 'A' || alpha2[0] > 'Z' || alpha2[1] < 'A' || alpha2[1] > 'Z' {
		return ""
	}
	return string([]rune{rune(alpha2[0]-'A') + 0x1F1E6, rune(alpha2[1]-'A') + 0x1F1E6})
}

func pow10(power int) int64 {
	result := int64(1)
	for range power {
		result *= 10
	}
	return result
}

// Stats gathers per-source status for the admin page and the public site.
func (db *DB) Stats(ctx context.Context) (model.Stats, error) {
	var stats model.Stats
	rows, err := db.sql.QueryContext(ctx, `
		SELECT id, repository, branch, path, priority, status, current_sha, record_count, skipped_rows,
		       last_checked_at, last_synced_at, last_error
		FROM sources WHERE enabled = 1 ORDER BY priority DESC, id`)
	if err != nil {
		return stats, err
	}
	defer rows.Close()
	for rows.Next() {
		var source model.SourceStatus
		var lastCheckedAt, lastSyncedAt sql.NullString
		if err := rows.Scan(&source.ID, &source.Repository, &source.Branch, &source.Path, &source.Priority,
			&source.Status, &source.CurrentCommit, &source.RecordCount, &source.SkippedRows,
			&lastCheckedAt, &lastSyncedAt, &source.LastError); err != nil {
			return stats, err
		}
		if lastCheckedAt.Valid {
			if parsed, parseErr := parseTime(lastCheckedAt.String); parseErr == nil {
				source.LastCheckedAt = &parsed
			}
		}
		if lastSyncedAt.Valid {
			if parsed, parseErr := parseTime(lastSyncedAt.String); parseErr == nil {
				source.LastSyncedAt = &parsed
			}
		}
		stats.Records += source.RecordCount
		if source.LastSyncedAt != nil && (stats.LastSyncedAt == nil || source.LastSyncedAt.After(*stats.LastSyncedAt)) {
			value := *source.LastSyncedAt
			stats.LastSyncedAt = &value
		}
		stats.Sources = append(stats.Sources, source)
	}
	if err := rows.Err(); err != nil {
		return stats, err
	}
	if stats.Sources == nil {
		stats.Sources = make([]model.SourceStatus, 0)
	}
	return stats, nil
}

// Ready reports whether at least one enabled source has completed an import,
// which is the condition under which lookups can be served.
func (db *DB) Ready(ctx context.Context) error {
	var ready bool
	if err := db.sql.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sources WHERE enabled = 1 AND status = 'ready' AND record_count > 0)`).Scan(&ready); err != nil {
		return err
	}
	if !ready {
		return errors.New("no source has completed its first sync")
	}
	return nil
}
