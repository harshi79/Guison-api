package model

import "time"

type Match struct {
	Start  string `json:"start"`
	End    string `json:"end"`
	Length int16  `json:"length"`
}

type Number struct {
	Length *int16 `json:"length"`
	Luhn   *bool  `json:"luhn"`
}

type Country struct {
	Alpha2    string   `json:"alpha2"`
	Alpha3    string   `json:"alpha3"`
	Name      string   `json:"name"`
	Emoji     string   `json:"emoji"`
	Currency  string   `json:"currency"`
	Latitude  *float64 `json:"latitude"`
	Longitude *float64 `json:"longitude"`
}

type Bank struct {
	Name  string `json:"name"`
	URL   string `json:"url"`
	Phone string `json:"phone"`
	City  string `json:"city"`
	Logo  string `json:"logo"`
}

type Attribution struct {
	ID         string    `json:"id"`
	Repository string    `json:"repository"`
	Commit     string    `json:"commit"`
	SyncedAt   time.Time `json:"synced_at"`
}

type LookupResult struct {
	IIN     string      `json:"iin"`
	Match   Match       `json:"match"`
	Number  Number      `json:"number"`
	Scheme  string      `json:"scheme"`
	Brand   string      `json:"brand"`
	Type    string      `json:"type"`
	Level   string      `json:"level"`
	Prepaid *bool       `json:"prepaid"`
	Country Country     `json:"country"`
	Bank    Bank        `json:"bank"`
	Source  Attribution `json:"source"`
}

type SourceStatus struct {
	ID            string     `json:"id"`
	Repository    string     `json:"repository"`
	Branch        string     `json:"branch"`
	Path          string     `json:"path"`
	Priority      int        `json:"priority"`
	Status        string     `json:"status"`
	CurrentCommit string     `json:"current_commit"`
	RecordCount   int64      `json:"record_count"`
	SkippedRows   int64      `json:"skipped_rows"`
	LastCheckedAt *time.Time `json:"last_checked_at"`
	LastSyncedAt  *time.Time `json:"last_synced_at"`
	LastError     string     `json:"last_error,omitempty"`
}

type Stats struct {
	Records      int64          `json:"records"`
	LastSyncedAt *time.Time     `json:"last_synced_at"`
	Sources      []SourceStatus `json:"sources"`
}
