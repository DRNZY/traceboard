package store

import (
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"

	_ "modernc.org/sqlite"
)

type Store struct {
	db        *sql.DB
	migrateMu sync.Mutex
	closeOnce sync.Once
	closeErr  error
}

func Open(path string) (*Store, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("database path is required")
	}

	absolutePath, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve database path: %w", err)
	}
	if err := ensurePrivateFile(absolutePath); err != nil {
		return nil, err
	}
	dsnURL := url.URL{Scheme: "file", Path: absolutePath}
	query := dsnURL.Query()
	query.Add("_pragma", "foreign_keys(1)")
	query.Add("_pragma", "busy_timeout(5000)")
	dsnURL.RawQuery = query.Encode()

	db, err := sql.Open("sqlite", dsnURL.String())
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(4)

	store := &Store{db: db}
	if _, err := db.Exec("PRAGMA journal_mode = WAL"); err != nil {
		store.Close()
		return nil, fmt.Errorf("enable WAL: %w", err)
	}
	var foreignKeys int
	if err := db.QueryRow("PRAGMA foreign_keys").Scan(&foreignKeys); err != nil {
		store.Close()
		return nil, fmt.Errorf("verify foreign keys: %w", err)
	}
	if foreignKeys != 1 {
		store.Close()
		return nil, errors.New("enable foreign keys")
	}
	if err := ensurePrivateFile(absolutePath); err != nil {
		store.Close()
		return nil, err
	}
	return store, nil
}

// ensurePrivateFile creates the database with user-only permissions before any
// connection can touch it, and repairs the mode of an existing file.
func ensurePrivateFile(path string) error {
	directory := filepath.Dir(path)
	if info, err := os.Stat(directory); err != nil {
		return fmt.Errorf("stat database directory: %w", err)
	} else if info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("database directory mode must exclude group and other access, got %04o", info.Mode().Perm())
	}
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return fmt.Errorf("create database file: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close database file: %w", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("secure database file: %w", err)
	}
	return nil
}

func (store *Store) Close() error {
	store.closeOnce.Do(func() {
		store.closeErr = store.db.Close()
	})
	return store.closeErr
}
