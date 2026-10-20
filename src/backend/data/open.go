package data

import (
	"context"
	"database/sql"
	"os"

	_ "modernc.org/sqlite"
)

// Open connects to the SQLite file at path and applies pending migrations from migrationsDir.
func Open(ctx context.Context, path, migrationsDir string) (*sql.DB, error) {
	// journal_mode=DELETE: WAL is unsafe on NFS/EFS.
	d, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(DELETE)&_pragma=foreign_keys(1)")
	if err != nil {
		return nil, err
	}
	d.SetMaxOpenConns(1) // one connection per instance; reserved concurrency caps total writers
	if _, err = Migrate(ctx, d, os.DirFS(migrationsDir), path); err != nil {
		d.Close()
		return nil, err
	}
	return d, nil
}
