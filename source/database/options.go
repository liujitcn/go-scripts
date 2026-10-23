package database

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

type Option func(*configOptions)

type configOptions struct {
	driver          string        // database driver: "mysql", "postgres", "sqlite3", etc.
	dsn             string        // data source name
	db              *sql.DB       // pre-existing *sql.DB (for shared connection pools)
	table           string        // table name (default "scripts")
	keyColumn       string        // key column (default "name")
	valueColumn     string        // value column (default "content")
	checksumColumn  string        // checksum column for change detection (default "updated_at")
	query           string        // custom SQL query (overrides auto-generated)
	prefix          string        // key prefix
	pollInterval    time.Duration // polling interval for Watch (default 10s)
	maxOpenConns    int           // max open connections
	maxIdleConns    int           // max idle connections
	connMaxLifetime time.Duration // connection max lifetime

	api dbAPI
}

type dbAPI interface {
	GetScript(ctx context.Context, key string) (value string, checksum string, err error)

	Close() error
}

type ioCloser interface {
	Close() error
}

func WithDriver(driver string) Option {
	return func(o *configOptions) { o.driver = driver }
}

func WithDSN(dsn string) Option {
	return func(o *configOptions) { o.dsn = dsn }
}

func WithDB(db *sql.DB) Option {
	return func(o *configOptions) { o.db = db }
}

func WithTable(table string) Option {
	return func(o *configOptions) { o.table = table }
}

func WithKeyColumn(col string) Option {
	return func(o *configOptions) { o.keyColumn = col }
}

func WithValueColumn(col string) Option {
	return func(o *configOptions) { o.valueColumn = col }
}

func WithChecksumColumn(col string) Option {
	return func(o *configOptions) { o.checksumColumn = col }
}

//

//

//

//

func WithQuery(query string) Option {
	return func(o *configOptions) { o.query = query }
}

//

func WithPrefix(prefix string) Option {
	return func(o *configOptions) {
		prefix = strings.TrimPrefix(prefix, "/")
		o.prefix = prefix
	}
}

func WithPollInterval(d time.Duration) Option {
	return func(o *configOptions) { o.pollInterval = d }
}

func WithMaxOpenConns(n int) Option {
	return func(o *configOptions) { o.maxOpenConns = n }
}

func WithMaxIdleConns(n int) Option {
	return func(o *configOptions) { o.maxIdleConns = n }
}

func WithConnMaxLifetime(d time.Duration) Option {
	return func(o *configOptions) { o.connMaxLifetime = d }
}
