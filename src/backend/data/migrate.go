// Package data holds the SQLite migration runner.
package data

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"os"
	"strings"
)

// Migrate applies pending NNN_name.sql files from fsys in one transaction, tracked in PRAGMA user_version.
// Returns the resulting schema version. Never edit or renumber a deployed migration; add a new file.
func Migrate(ctx context.Context, db *sql.DB, fsys fs.FS, dbPath string) (version int, err error) {
	files, err := fs.Glob(fsys, "*.sql") // sorted by name
	if err != nil {
		return 0, err
	}
	c, err := db.Conn(ctx) // PRAGMAs and BEGIN must stay on one connection
	if err != nil {
		return 0, err
	}
	defer c.Close()

	if err = c.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return 0, err
	}
	if version > len(files) {
		return 0, fmt.Errorf("database is at version %d but this build only knows %d: refusing to run older code on a newer schema", version, len(files))
	}
	if version == len(files) {
		return version, nil
	}

	// Off during migrations so table rebuilds don't cascade; can't be changed inside a transaction.
	if _, err = c.ExecContext(ctx, "PRAGMA foreign_keys=OFF"); err != nil {
		return 0, err
	}
	defer c.ExecContext(context.Background(), "PRAGMA foreign_keys=ON")
	// Write lock: a concurrent Lambda waits (busy_timeout), then sees the new version.
	if _, err = c.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return 0, err
	}
	defer func() {
		if err != nil {
			c.ExecContext(context.Background(), "ROLLBACK")
		}
	}()

	if err = c.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return 0, err
	}
	if dbPath != "" && version > 0 && version < len(files) {
		if err = copyFile(dbPath, fmt.Sprintf("%s.v%d.bak", dbPath, version)); err != nil {
			return 0, err
		}
	}
	for i := version; i < len(files); i++ {
		var b []byte
		if b, err = fs.ReadFile(fsys, files[i]); err != nil {
			return 0, err
		}
		// The driver runs every statement in the string, triggers included.
		if _, err = c.ExecContext(ctx, strings.TrimPrefix(string(b), "\ufeff")); err != nil {
			return 0, fmt.Errorf("%s: %w", files[i], err)
		}
		if _, err = c.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", i+1)); err != nil {
			return 0, err
		}
	}

	rows, err := c.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		return 0, err
	}
	broken := rows.Next()
	rows.Close()
	if broken {
		err = fmt.Errorf("migration left foreign key violations")
		return 0, err
	}
	if _, err = c.ExecContext(ctx, "COMMIT"); err != nil {
		return 0, err
	}
	return len(files), nil
}

func copyFile(src, dst string) error {
	b, err := os.ReadFile(src) // ~10 MB, fine in memory
	if err != nil {
		return err
	}
	return os.WriteFile(dst, b, 0o640)
}
