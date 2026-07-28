package database

import (
	"context"
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
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrations embed.FS

type DB struct {
	Pool *pgxpool.Pool
}

func Open(ctx context.Context, databaseURL string, minConns, maxConns int32) (*DB, error) {
	poolConfig, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse DATABASE_URL: %w", err)
	}
	poolConfig.MinConns = minConns
	poolConfig.MaxConns = maxConns
	poolConfig.MaxConnLifetime = time.Hour
	poolConfig.MaxConnIdleTime = 15 * time.Minute
	poolConfig.HealthCheckPeriod = 30 * time.Second
	poolConfig.ConnConfig.RuntimeParams["application_name"] = "open-bin-api"

	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return nil, fmt.Errorf("create database pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("connect to database: %w", err)
	}
	db := &DB{Pool: pool}
	if err := db.Migrate(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return db, nil
}

func (db *DB) Close() { db.Pool.Close() }

func (db *DB) Migrate(ctx context.Context) error {
	conn, err := db.Pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock(684429910224831)`); err != nil {
		return fmt.Errorf("acquire migration lock: %w", err)
	}
	defer func() {
		_, _ = conn.Exec(context.Background(), `SELECT pg_advisory_unlock(684429910224831)`)
	}()

	if _, err := conn.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (version text PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT now())`); err != nil {
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
		err := conn.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=$1)`, entry.Name()).Scan(&applied)
		if err != nil {
			return err
		}
		if applied {
			continue
		}
		sql, err := migrations.ReadFile("migrations/" + entry.Name())
		if err != nil {
			return err
		}
		tx, err := conn.Begin(ctx)
		if err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, string(sql)); err == nil {
			_, err = tx.Exec(ctx, `INSERT INTO schema_migrations(version) VALUES($1)`, entry.Name())
		}
		if err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("apply migration %s: %w", entry.Name(), err)
		}
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("commit migration %s: %w", entry.Name(), err)
		}
		slog.Info("database migration applied", "version", entry.Name())
	}
	return nil
}

func (db *DB) ConfigureSources(ctx context.Context, sources []config.Source) error {
	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `UPDATE sources SET enabled=false, updated_at=now()`); err != nil {
		return err
	}
	for _, source := range sources {
		_, err := tx.Exec(ctx, `
			INSERT INTO sources(id, repository, branch, path, parser, priority, enabled)
			VALUES($1,$2,$3,$4,$5,$6,true)
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
				enabled=true,
				last_error='',
				updated_at=now()`,
			source.ID, source.Repository, source.Branch, source.Path, source.Format, source.Priority)
		if err != nil {
			return fmt.Errorf("configure source %s: %w", source.ID, err)
		}
	}
	return tx.Commit(ctx)
}

func (db *DB) Lookup(ctx context.Context, iin string) (*model.LookupResult, error) {
	start, end, err := NormalizeQuery(iin)
	if err != nil {
		return nil, err
	}
	var result model.LookupResult
	result.IIN = iin
	err = db.Pool.QueryRow(ctx, `
		SELECT b.iin_start, b.iin_end, b.iin_length,
		       b.number_length, b.luhn, b.scheme, b.brand, b.card_type, b.card_level, b.prepaid,
		       b.country_alpha2, b.country_alpha3, b.country_name, b.country_currency,
		       b.country_latitude, b.country_longitude,
		       b.bank_name, b.bank_url, b.bank_phone, b.bank_city, b.bank_logo,
		       s.id, s.repository, s.current_sha, s.last_synced_at
		FROM bin_records b
		JOIN sources s ON s.id=b.source_id AND s.enabled=true AND s.status='ready'
		WHERE int8range(b.start8, b.end8, '[]') @> $1::bigint
		  AND int8range(b.start8, b.end8, '[]') @> $2::bigint
		ORDER BY (b.end8-b.start8) ASC, b.iin_length DESC, s.priority DESC, b.updated_at DESC
		LIMIT 1`, start, end).Scan(
		&result.Match.Start, &result.Match.End, &result.Match.Length,
		&result.Number.Length, &result.Number.Luhn, &result.Scheme, &result.Brand, &result.Type, &result.Level, &result.Prepaid,
		&result.Country.Alpha2, &result.Country.Alpha3, &result.Country.Name, &result.Country.Currency,
		&result.Country.Latitude, &result.Country.Longitude,
		&result.Bank.Name, &result.Bank.URL, &result.Bank.Phone, &result.Bank.City, &result.Bank.Logo,
		&result.Source.ID, &result.Source.Repository, &result.Source.Commit, &result.Source.SyncedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("lookup iin: %w", err)
	}
	result.Country.Emoji = CountryEmoji(result.Country.Alpha2)
	return &result, nil
}

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

func (db *DB) Stats(ctx context.Context) (model.Stats, error) {
	var stats model.Stats
	rows, err := db.Pool.Query(ctx, `
		SELECT id, repository, branch, path, priority, status, current_sha, record_count, skipped_rows,
		       last_checked_at, last_synced_at, last_error
		FROM sources WHERE enabled=true ORDER BY priority DESC, id`)
	if err != nil {
		return stats, err
	}
	defer rows.Close()
	for rows.Next() {
		var source model.SourceStatus
		if err := rows.Scan(&source.ID, &source.Repository, &source.Branch, &source.Path, &source.Priority,
			&source.Status, &source.CurrentCommit, &source.RecordCount, &source.SkippedRows,
			&source.LastCheckedAt, &source.LastSyncedAt, &source.LastError); err != nil {
			return stats, err
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

func (db *DB) Ready(ctx context.Context) error {
	var ready bool
	if err := db.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM sources WHERE enabled=true AND status='ready' AND record_count>0)`).Scan(&ready); err != nil {
		return err
	}
	if !ready {
		return errors.New("no source has completed its first sync")
	}
	return nil
}
