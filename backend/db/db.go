// Package db handles SQLite connection, schema migration, and initial seeding.
package db

import (
	"database/sql"
	_ "embed"
	"fmt"
	"log"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schemaSQL string

// Open creates (or opens) the SQLite database at dataDir/meds.db,
// applies the schema, enables WAL mode & foreign keys, and seeds if empty.
func Open(dataDir string) (*sql.DB, error) {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}

	dsn := filepath.Join(dataDir, "meds.db") + "?_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	// Single-connection pool for SQLite.
	db.SetMaxOpenConns(1)

	if err := migrate(db); err != nil {
		db.Close()
		return nil, err
	}

	return db, nil
}

func migrate(db *sql.DB) error {
	if _, err := db.Exec(schemaSQL); err != nil {
		return fmt.Errorf("apply schema: %w", err)
	}

	// Migration: add start_date/end_date to schedules if missing.
	for _, col := range []struct{ name, ddl string }{
		{"start_date", `ALTER TABLE schedules ADD COLUMN start_date TEXT NOT NULL DEFAULT '2000-01-01'`},
		{"end_date", `ALTER TABLE schedules ADD COLUMN end_date TEXT`},
	} {
		var dummy string
		err := db.QueryRow(`SELECT ` + col.name + ` FROM schedules LIMIT 1`).Scan(&dummy)
		if err != nil {
			if _, err2 := db.Exec(col.ddl); err2 != nil {
				log.Printf("migration %s: %v (may already exist)", col.name, err2)
			}
		}
	}

	return nil
}
