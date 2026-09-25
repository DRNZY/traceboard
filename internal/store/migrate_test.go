package store

import (
	"context"
	"database/sql"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"traceboard/migrations"
)

func TestMigrateCreatesRequiredSchemaAndSQLiteFeatures(t *testing.T) {
	store := openMigratedStore(t)
	defer store.Close()

	var journalMode string
	if err := store.db.QueryRow("PRAGMA journal_mode").Scan(&journalMode); err != nil {
		t.Fatalf("query journal mode: %v", err)
	}
	if journalMode != "wal" {
		t.Fatalf("journal mode = %q, want wal", journalMode)
	}

	var foreignKeys int
	if err := store.db.QueryRow("PRAGMA foreign_keys").Scan(&foreignKeys); err != nil {
		t.Fatalf("query foreign keys: %v", err)
	}
	if foreignKeys != 1 {
		t.Fatalf("foreign_keys = %d, want 1", foreignKeys)
	}

	wantTables := []string{
		"schema_migrations",
		"sources",
		"projects",
		"runs",
		"steps",
		"events",
		"events_fts",
		"alerts",
		"quarantine",
		"sessions",
	}
	for _, table := range wantTables {
		var name string
		err := store.db.QueryRow("SELECT name FROM sqlite_master WHERE name = ?", table).Scan(&name)
		if err != nil {
			t.Fatalf("table %s: %v", table, err)
		}
	}

	assertUniqueIndex(t, store.db, "events", "ux_events_source_source_event_id", []string{"source", "source_event_id"})
	assertUniqueIndex(t, store.db, "events", "ux_events_run_sequence", []string{"run_id", "sequence"})

	for _, table := range []string{"steps", "events", "alerts"} {
		assertRunCascadeForeignKey(t, store.db, table)
	}

	if _, err := store.db.Exec("INSERT INTO events_fts(rowid, run_id, text) VALUES (1, 'run_fts', 'searchable failure text')"); err != nil {
		t.Fatalf("insert FTS5 row: %v", err)
	}
	var runID string
	if err := store.db.QueryRow("SELECT run_id FROM events_fts WHERE events_fts MATCH 'searchable'").Scan(&runID); err != nil {
		t.Fatalf("query FTS5 row: %v", err)
	}
	if runID != "run_fts" {
		t.Fatalf("FTS run id = %q, want run_fts", runID)
	}
}

func TestMigrateIsIdempotent(t *testing.T) {
	store := openMigratedStore(t)
	defer store.Close()

	if err := store.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate again: %v", err)
	}

	var count int
	if err := store.db.QueryRow("SELECT COUNT(*) FROM schema_migrations").Scan(&count); err != nil {
		t.Fatalf("count schema migrations: %v", err)
	}
	if count != embeddedMigrationCount(t) {
		t.Fatalf("schema migration count = %d, want %d", count, embeddedMigrationCount(t))
	}
	for _, table := range []string{"schema_migrations"} {
		if _, err := store.db.Query("SELECT 1 FROM " + table + " LIMIT 1"); err != nil {
			t.Fatalf("table %s: %v", table, err)
		}
	}
}

func embeddedMigrationCount(t *testing.T) int {
	t.Helper()
	entries, err := fs.ReadDir(migrations.Files, ".")
	if err != nil {
		t.Fatalf("read embedded migrations: %v", err)
	}
	count := 0
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".sql") {
			count++
		}
	}
	return count
}

func TestMigrateRejectsAppliedMigrationEdit(t *testing.T) {
	store := openStoreWithoutMigration(t)
	defer store.Close()

	files := fstest.MapFS{
		"001_probe.sql": &fstest.MapFile{Data: []byte("CREATE TABLE probe (id INTEGER PRIMARY KEY);")},
	}
	if err := migrate(context.Background(), store.db, files); err != nil {
		t.Fatalf("apply probe migration: %v", err)
	}

	files["001_probe.sql"] = &fstest.MapFile{Data: []byte("CREATE TABLE changed (id INTEGER PRIMARY KEY);")}
	err := migrate(context.Background(), store.db, files)
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("expected checksum mismatch, got %v", err)
	}
}

func openMigratedStore(t *testing.T) *Store {
	t.Helper()
	store := openStoreWithoutMigration(t)
	if err := store.Migrate(context.Background()); err != nil {
		store.Close()
		t.Fatalf("migrate: %v", err)
	}
	return store
}

func openStoreWithoutMigration(t *testing.T) *Store {
	t.Helper()
	store, err := Open(privateTempDatabase(t))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	return store
}

// privateTempDatabase returns a database path inside a user-only directory,
// matching the permission floor the store enforces in production.
func privateTempDatabase(t *testing.T) string {
	t.Helper()
	directory := t.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		t.Fatalf("secure temp directory: %v", err)
	}
	return filepath.Join(directory, "traceboard.db")
}

func assertUniqueIndex(t *testing.T, db *sql.DB, table, wantName string, wantColumns []string) {
	t.Helper()
	rows, err := db.Query("PRAGMA index_list(" + table + ")")
	if err != nil {
		t.Fatalf("list %s indexes: %v", table, err)
	}
	defer rows.Close()

	for rows.Next() {
		var sequence int
		var name string
		var unique int
		var origin string
		var partial int
		if err := rows.Scan(&sequence, &name, &unique, &origin, &partial); err != nil {
			t.Fatalf("scan %s index: %v", table, err)
		}
		if name != wantName {
			continue
		}
		if unique != 1 {
			t.Fatalf("index %s unique = %d, want 1", name, unique)
		}
		columnRows, err := db.Query("PRAGMA index_info(" + name + ")")
		if err != nil {
			t.Fatalf("inspect %s: %v", name, err)
		}
		defer columnRows.Close()
		var gotColumns []string
		for columnRows.Next() {
			var index int
			var columnID int
			var columnName string
			if err := columnRows.Scan(&index, &columnID, &columnName); err != nil {
				t.Fatalf("scan %s column: %v", name, err)
			}
			gotColumns = append(gotColumns, columnName)
		}
		if strings.Join(gotColumns, ",") != strings.Join(wantColumns, ",") {
			t.Fatalf("index %s columns = %v, want %v", name, gotColumns, wantColumns)
		}
		return
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate %s indexes: %v", table, err)
	}
	t.Fatalf("index %s not found", wantName)
}

func assertRunCascadeForeignKey(t *testing.T, db *sql.DB, table string) {
	t.Helper()
	rows, err := db.Query("PRAGMA foreign_key_list(" + table + ")")
	if err != nil {
		t.Fatalf("list %s foreign keys: %v", table, err)
	}
	defer rows.Close()

	for rows.Next() {
		var id int
		var sequence int
		var targetTable string
		var fromColumn string
		var targetColumn string
		var onUpdate string
		var onDelete string
		var match string
		if err := rows.Scan(&id, &sequence, &targetTable, &fromColumn, &targetColumn, &onUpdate, &onDelete, &match); err != nil {
			t.Fatalf("scan %s foreign key: %v", table, err)
		}
		if targetTable == "runs" && onDelete == "CASCADE" {
			return
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate %s foreign keys: %v", table, err)
	}
	t.Fatalf("%s run-owned cascade foreign key not found", table)
}
