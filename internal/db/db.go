package db

import (
	"context"
	"database/sql"
	"time"

	_ "github.com/lib/pq"
)

type PoolOptions struct {
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration
}

func Connect(connString string, options ...PoolOptions) (*sql.DB, error) {
	database, err := sql.Open("postgres", connString)
	if err != nil {
		return nil, err
	}
	if len(options) > 0 {
		pool := options[0]
		if pool.MaxOpenConns > 0 {
			database.SetMaxOpenConns(pool.MaxOpenConns)
		}
		if pool.MaxIdleConns >= 0 {
			database.SetMaxIdleConns(pool.MaxIdleConns)
		}
		if pool.ConnMaxLifetime > 0 {
			database.SetConnMaxLifetime(pool.ConnMaxLifetime)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err = database.PingContext(ctx); err != nil {
		_ = database.Close()
		return nil, err
	}

	return database, nil
}
