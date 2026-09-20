package database

import (
	// The libsql driver used in production is remote-only (libsql://,
	// https://, wss://, or file://). Its file:// mode delegates to whichever
	// "sqlite" or "sqlite3" driver is registered. Registering the CGO-based
	// sqlite3 driver here gives the test suite a real, local SQLite engine
	// (in-memory and on-disk), while the production binary compiled by
	// `CGO_ENABLED=0 go build` never links SQLite and never registers this
	// driver.
	_ "github.com/mattn/go-sqlite3"
)
