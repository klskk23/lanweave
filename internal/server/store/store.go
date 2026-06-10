// Package store owns the SQLite database: opening it, running migrations, and
// exposing typed repositories. SQLite is the single source of truth.
package store

import (
	"database/sql"
	"embed"
	"fmt"
	"log/slog"

	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

type Store struct {
	db *sql.DB
}

// Open opens (or creates) the SQLite database at dbPath with WAL journaling,
// a busy timeout, and foreign keys enabled.
func Open(dbPath string) (*Store, error) {
	// _txlock=immediate makes every BeginTx take the writer lock up front, so
	// concurrent write transactions queue on busy_timeout instead of failing
	// SQLITE_BUSY at the deferred read→write upgrade (announcement allocation
	// runs read-then-insert in one transaction and hits exactly that). The flip
	// side: BeginTx is for WRITE transactions only — a pure read wrapped in
	// BeginTx would needlessly serialize all writers; plain Query/QueryRow
	// already give consistent snapshots.
	dsn := fmt.Sprintf("file:%s?_txlock=immediate&_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)", dbPath)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite %q: %w", dbPath, err)
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("open sqlite %q: %w", dbPath, err)
	}
	return &Store{db: db}, nil
}

// DB exposes the underlying handle for repositories and tests.
func (s *Store) DB() *sql.DB { return s.db }

// Close releases the database handle.
func (s *Store) Close() error { return s.db.Close() }

// Migrate brings the schema to the latest version. It is idempotent: already
// applied migrations are skipped. Each migration run is logged.
func (s *Store) Migrate(log *slog.Logger) error {
	goose.SetBaseFS(migrationsFS)
	goose.SetLogger(goose.NopLogger())
	if err := goose.SetDialect("sqlite3"); err != nil {
		return fmt.Errorf("set goose dialect: %w", err)
	}
	before, err := goose.GetDBVersion(s.db)
	if err != nil {
		return fmt.Errorf("read db version: %w", err)
	}
	if err := goose.Up(s.db, "migrations"); err != nil {
		return fmt.Errorf("run migrations: %w", err)
	}
	after, err := goose.GetDBVersion(s.db)
	if err != nil {
		return fmt.Errorf("read db version: %w", err)
	}
	if log != nil {
		if after > before {
			log.Info("migrations applied", "from_version", before, "to_version", after)
		} else {
			log.Info("migrations up to date", "version", after)
		}
	}
	return nil
}
