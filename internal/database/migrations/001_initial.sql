-- Schema for SQLite (Turso/libSQL).
--
-- This mirrors the logical structure previously stored in PostgreSQL, using
-- SQLite-compatible types. Lookup does not use PostgreSQL int8range/GiST;
-- instead it relies on the regular B-tree index over (source_id, iin_length,
-- start8) plus a per-row containment predicate. See internal/database/database.go.

CREATE TABLE IF NOT EXISTS sources (
    id              TEXT PRIMARY KEY,
    repository      TEXT NOT NULL DEFAULT '',
    branch          TEXT NOT NULL DEFAULT '',
    path            TEXT NOT NULL DEFAULT '',
    parser          TEXT NOT NULL DEFAULT '',
    priority        INTEGER NOT NULL DEFAULT 0,
    enabled         INTEGER NOT NULL DEFAULT 1,
    current_sha     TEXT NOT NULL DEFAULT '',
    status          TEXT NOT NULL DEFAULT 'pending',
    record_count    INTEGER NOT NULL DEFAULT 0,
    skipped_rows    INTEGER NOT NULL DEFAULT 0,
    last_checked_at TEXT,
    last_synced_at  TEXT,
    last_error      TEXT NOT NULL DEFAULT '',
    created_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    updated_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

CREATE TABLE IF NOT EXISTS bin_records (
    source_id        TEXT NOT NULL REFERENCES sources(id) ON DELETE CASCADE,
    iin_start        TEXT NOT NULL,
    iin_end          TEXT NOT NULL,
    iin_length       INTEGER NOT NULL,
    start8           INTEGER NOT NULL,
    end8             INTEGER NOT NULL,
    number_length    INTEGER,
    luhn             INTEGER,
    scheme           TEXT NOT NULL DEFAULT '',
    brand            TEXT NOT NULL DEFAULT '',
    card_type        TEXT NOT NULL DEFAULT '',
    card_level       TEXT NOT NULL DEFAULT '',
    prepaid          INTEGER,
    country_alpha2   TEXT NOT NULL DEFAULT '',
    country_alpha3   TEXT NOT NULL DEFAULT '',
    country_name     TEXT NOT NULL DEFAULT '',
    country_currency TEXT NOT NULL DEFAULT '',
    country_latitude REAL,
    country_longitude REAL,
    bank_name        TEXT NOT NULL DEFAULT '',
    bank_url         TEXT NOT NULL DEFAULT '',
    bank_phone       TEXT NOT NULL DEFAULT '',
    bank_city        TEXT NOT NULL DEFAULT '',
    bank_logo        TEXT NOT NULL DEFAULT '',
    updated_at       TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    PRIMARY KEY (source_id, iin_start, iin_end),
    CONSTRAINT bin_records_iin_digits CHECK (length(iin_start) BETWEEN 1 AND 8 AND length(iin_end) BETWEEN 1 AND 8),
    CONSTRAINT bin_records_range_valid CHECK (start8 >= 0 AND end8 >= start8 AND end8 <= 99999999),
    CONSTRAINT bin_records_iin_length CHECK (iin_length BETWEEN 1 AND 8)
) WITHOUT ROWID;

-- Fast range lookups. A lookup matches a record whose [start8, end8] range
-- contains the query's 8-digit expansion. Records are split into two shapes
-- that are queried by index:
--
--   1. Single-BIN rows (iin_start = iin_end, the overwhelmingly common case,
--      e.g. every bin-list-data row): a single row covers exactly the one
--      8-digit value spelled by its iin_start prefix, so containment reduces
--      to an exact-match lookup of the query's 1..8 digit prefixes.
--   2. True range rows (iin_start <> iin_end): matched by the containment
--      predicate start8 <= qStart AND end8 >= qEnd.
--
-- See Lookup in internal/database/database.go for the matching UNION ALL
-- query. Partial indexes keep the single-row index tiny and let SQLite seek
-- both shapes without scanning the table or spilling a temp sort.
CREATE INDEX IF NOT EXISTS bin_records_single_idx ON bin_records (iin_start) WHERE iin_start = iin_end;
CREATE INDEX IF NOT EXISTS bin_records_range_idx ON bin_records (source_id, start8) WHERE iin_start <> iin_end;
CREATE INDEX IF NOT EXISTS bin_records_source_idx ON bin_records (source_id);
CREATE INDEX IF NOT EXISTS sources_enabled_priority_idx ON sources (enabled, priority DESC);
