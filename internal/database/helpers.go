package database

import (
	"database/sql/driver"
	"fmt"
	"strings"
	"time"

	libsql "github.com/tursodatabase/libsql-client-go/libsql"
)

// libsqlConnector builds a database/sql driver.Connector for the configured
// libSQL/Turso database. The authentication token is never embedded in the
// connection string; it is attached through the driver's first-class option
// so it never appears in logs, errors, or page copy.
func libsqlConnector(databaseURL, authToken string) (driver.Connector, error) {
	url := strings.TrimSpace(databaseURL)
	if url == "" {
		return nil, fmt.Errorf("TURSO_DATABASE_URL is required")
	}
	token := strings.TrimSpace(authToken)
	if token == "" {
		return nil, fmt.Errorf("TURSO_AUTH_TOKEN is required")
	}
	// libsql.NewConnector validates the URL scheme and forbidden query
	// parameters, and turns the token into an Authorization: Bearer header.
	return libsql.NewConnector(url, libsql.WithAuthToken(token))
}

// NowSQL renders the current UTC time in the RFC 3339 layout used to store
// every timestamp column. A single canonical format keeps reads and writes
// across both the database layer and the syncer consistent and
// lexicographically ordered (so updated_at DESC sorts work).
func NowSQL() string {
	return time.Now().UTC().Format(time.RFC3339Nano)
}

// parseTime parses the RFC 3339 timestamps written by NowSQL. An empty value
// yields a zero time.
func parseTime(value string) (time.Time, error) {
	return time.Parse(time.RFC3339Nano, value)
}
