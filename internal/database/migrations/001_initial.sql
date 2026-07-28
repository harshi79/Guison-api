CREATE TABLE IF NOT EXISTS sources (
    id              text PRIMARY KEY,
    repository      text NOT NULL,
    branch          text NOT NULL,
    path            text NOT NULL,
    parser          text NOT NULL,
    priority        integer NOT NULL DEFAULT 0,
    enabled         boolean NOT NULL DEFAULT true,
    current_sha     text NOT NULL DEFAULT '',
    status          text NOT NULL DEFAULT 'pending',
    record_count    bigint NOT NULL DEFAULT 0,
    skipped_rows    bigint NOT NULL DEFAULT 0,
    last_checked_at timestamptz,
    last_synced_at  timestamptz,
    last_error      text NOT NULL DEFAULT '',
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS bin_records (
    source_id       text NOT NULL REFERENCES sources(id) ON DELETE CASCADE,
    iin_start       text NOT NULL,
    iin_end         text NOT NULL,
    iin_length      smallint NOT NULL,
    start8          bigint NOT NULL,
    end8            bigint NOT NULL,
    number_length   smallint,
    luhn            boolean,
    scheme          text NOT NULL DEFAULT '',
    brand           text NOT NULL DEFAULT '',
    card_type       text NOT NULL DEFAULT '',
    card_level      text NOT NULL DEFAULT '',
    prepaid         boolean,
    country_alpha2  text NOT NULL DEFAULT '',
    country_alpha3  text NOT NULL DEFAULT '',
    country_name    text NOT NULL DEFAULT '',
    country_currency text NOT NULL DEFAULT '',
    country_latitude double precision,
    country_longitude double precision,
    bank_name       text NOT NULL DEFAULT '',
    bank_url        text NOT NULL DEFAULT '',
    bank_phone      text NOT NULL DEFAULT '',
    bank_city       text NOT NULL DEFAULT '',
    bank_logo       text NOT NULL DEFAULT '',
    updated_at      timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (source_id, iin_start, iin_end),
    CONSTRAINT bin_records_iin_digits CHECK (iin_start ~ '^[0-9]{1,8}$' AND iin_end ~ '^[0-9]{1,8}$'),
    CONSTRAINT bin_records_range_valid CHECK (start8 >= 0 AND end8 >= start8 AND end8 <= 99999999),
    CONSTRAINT bin_records_iin_length CHECK (iin_length BETWEEN 1 AND 8)
);

-- GiST makes containment queries independent of the size of the imported data.
CREATE INDEX IF NOT EXISTS bin_records_range_gist
    ON bin_records USING gist (int8range(start8, end8, '[]'));
CREATE INDEX IF NOT EXISTS bin_records_source_idx ON bin_records (source_id);
CREATE INDEX IF NOT EXISTS sources_enabled_priority_idx ON sources (enabled, priority DESC);
