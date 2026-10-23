package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

type sqlDB struct {
	db    *sql.DB
	query string // pre-built query (or custom query from config)
	owned bool   // true if this instance opened the DB and should Close it
}

func newSqlDB(cfg *configOptions) (*sqlDB, error) {

	query, err := buildQuery(cfg)
	if err != nil {
		return nil, err
	}

	if cfg.db != nil {
		return &sqlDB{
			db:    cfg.db,
			query: query,
			owned: false,
		}, nil
	}

	if cfg.driver == "" || cfg.dsn == "" {
		return nil, errors.New("database source: WithDriver and WithDSN are required (or use WithDB)")
	}

	db, err := sql.Open(cfg.driver, cfg.dsn)
	if err != nil {
		return nil, fmt.Errorf("database source: open %q: %w", cfg.driver, err)
	}

	if cfg.maxOpenConns > 0 {
		db.SetMaxOpenConns(cfg.maxOpenConns)
	}
	if cfg.maxIdleConns > 0 {
		db.SetMaxIdleConns(cfg.maxIdleConns)
	}
	if cfg.connMaxLifetime > 0 {
		db.SetConnMaxLifetime(cfg.connMaxLifetime)
	}

	return &sqlDB{
		db:    db,
		query: query,
		owned: true,
	}, nil
}

func (s *sqlDB) GetScript(ctx context.Context, key string) (string, string, error) {
	var value, checksum string
	err := s.db.QueryRowContext(ctx, s.query, key).Scan(&value, &checksum)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", "", fmt.Errorf("%w: %s", ErrNotFound, key)
		}
		return "", "", fmt.Errorf("database source: query %q: %w", key, err)
	}
	return value, checksum, nil
}

func (s *sqlDB) Close() error {
	if s.owned && s.db != nil {
		return s.db.Close()
	}
	return nil
}

var _ dbAPI = (*sqlDB)(nil)

func buildQuery(cfg *configOptions) (string, error) {

	if cfg.query != "" {
		return cfg.query, nil
	}

	table := cfg.table
	if table == "" {
		table = "scripts"
	}
	keyCol := cfg.keyColumn
	if keyCol == "" {
		keyCol = "name"
	}
	valueCol := cfg.valueColumn
	if valueCol == "" {
		valueCol = "content"
	}
	checksumCol := cfg.checksumColumn
	if checksumCol == "" {
		checksumCol = "updated_at"
	}

	return fmt.Sprintf("SELECT %s, %s FROM %s WHERE %s = ?",
		quoteIdent(valueCol), quoteIdent(checksumCol),
		quoteIdent(table), quoteIdent(keyCol)), nil
}

func quoteIdent(name string) string {
	return "\"" + name + "\""
}
