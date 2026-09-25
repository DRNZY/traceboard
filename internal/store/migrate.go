package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"strings"
	"time"

	"traceboard/migrations"
)

var migrationName = regexp.MustCompile(`^([0-9]{3})_[a-z0-9_]+\.sql$`)

func (store *Store) Migrate(ctx context.Context) error {
	store.migrateMu.Lock()
	defer store.migrateMu.Unlock()
	return migrate(ctx, store.db, migrations.Files)
}

func migrate(ctx context.Context, db *sql.DB, files fs.FS) error {
	if _, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			filename TEXT PRIMARY KEY,
			sha256 TEXT NOT NULL CHECK (length(sha256) = 64),
			applied_at INTEGER NOT NULL
		)`); err != nil {
		return fmt.Errorf("create schema migrations: %w", err)
	}

	entries, err := fs.ReadDir(files, ".")
	if err != nil {
		return fmt.Errorf("read embedded migrations: %w", err)
	}
	embedded := make(map[string][]byte)
	var filenames []string
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		if !migrationName.MatchString(entry.Name()) {
			return fmt.Errorf("invalid migration filename %q", entry.Name())
		}
		contents, err := fs.ReadFile(files, entry.Name())
		if err != nil {
			return fmt.Errorf("read migration %s: %w", entry.Name(), err)
		}
		embedded[entry.Name()] = contents
		filenames = append(filenames, entry.Name())
	}

	applied, err := loadAppliedMigrations(ctx, db)
	if err != nil {
		return err
	}
	for filename := range applied {
		if _, ok := embedded[filename]; !ok {
			return fmt.Errorf("applied migration %s is missing", filename)
		}
	}
	for _, filename := range filenames {
		contents := embedded[filename]
		hash := migrationHash(contents)
		if recordedHash, ok := applied[filename]; ok {
			if recordedHash != hash {
				return fmt.Errorf("migration %s checksum mismatch", filename)
			}
			continue
		}
		if err := applyMigration(ctx, db, filename, hash, contents); err != nil {
			return err
		}
	}
	return nil
}

func loadAppliedMigrations(ctx context.Context, db *sql.DB) (map[string]string, error) {
	rows, err := db.QueryContext(ctx, "SELECT filename, sha256 FROM schema_migrations")
	if err != nil {
		return nil, fmt.Errorf("query schema migrations: %w", err)
	}
	defer rows.Close()

	applied := make(map[string]string)
	for rows.Next() {
		var filename string
		var hash string
		if err := rows.Scan(&filename, &hash); err != nil {
			return nil, fmt.Errorf("scan schema migration: %w", err)
		}
		applied[filename] = hash
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate schema migrations: %w", err)
	}
	return applied, nil
}

func applyMigration(ctx context.Context, db *sql.DB, filename, hash string, contents []byte) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin migration %s: %w", filename, err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, string(contents)); err != nil {
		return fmt.Errorf("execute migration %s: %w", filename, err)
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations(filename, sha256, applied_at) VALUES (?, ?, ?)", filename, hash, time.Now().UTC().UnixNano()); err != nil {
		if isUniqueViolation(err) {
			return fmt.Errorf("migration %s was already applied", filename)
		}
		return fmt.Errorf("record migration %s: %w", filename, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migration %s: %w", filename, err)
	}
	return nil
}

func migrationHash(contents []byte) string {
	sum := sha256.Sum256(contents)
	return hex.EncodeToString(sum[:])
}

func isUniqueViolation(err error) bool {
	var sqliteError interface{ Error() string }
	if !errors.As(err, &sqliteError) {
		return false
	}
	return strings.Contains(sqliteError.Error(), "UNIQUE constraint failed")
}
