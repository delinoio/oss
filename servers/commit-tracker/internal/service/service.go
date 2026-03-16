package service

import (
	"database/sql"
	_ "embed"
	"fmt"
	"log/slog"

	"github.com/delinoio/oss/servers/commit-tracker/internal/contracts"

	_ "modernc.org/sqlite"
)

//go:embed migrations/001_initial.sql
var migrationSQL string

// Config holds the service configuration.
type Config struct {
	DatabaseURL string
	AuthToken   string
	GithubToken string
}

// Service holds the database connection and configuration for the commit-tracker API.
type Service struct {
	db     *sql.DB
	config Config
	logger *slog.Logger
}

// New creates a new Service, opens the SQLite database, and runs migrations.
func New(cfg Config, logger *slog.Logger) (*Service, error) {
	logger.Info("opening database", slog.String("event", contracts.EventDBOpen), slog.String("dsn", cfg.DatabaseURL))

	db, err := sql.Open("sqlite", cfg.DatabaseURL)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	// Enable WAL mode for better concurrent read performance.
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		db.Close()
		return nil, fmt.Errorf("set WAL mode: %w", err)
	}

	// Enable foreign keys.
	if _, err := db.Exec("PRAGMA foreign_keys=ON"); err != nil {
		db.Close()
		return nil, fmt.Errorf("enable foreign keys: %w", err)
	}

	logger.Info("running migrations", slog.String("event", contracts.EventDBMigrate))
	if _, err := db.Exec(migrationSQL); err != nil {
		db.Close()
		return nil, fmt.Errorf("run migrations: %w", err)
	}

	return &Service{
		db:     db,
		config: cfg,
		logger: logger,
	}, nil
}

// Close closes the underlying database connection.
func (s *Service) Close() error {
	s.logger.Info("closing database", slog.String("event", contracts.EventDBClose))
	return s.db.Close()
}
