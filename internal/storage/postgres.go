// Package storage persists aggregated ice statistics and detected ridges in
// PostgreSQL and serves historical time-range / geo-rectangle queries.
package storage

import (
	"context"
	"embed"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations_sql/*.sql
var migrationFS embed.FS

// Postgres is the persistent ice-condition store.
type Postgres struct {
	pool *pgxpool.Pool
}

// Connect opens a pgx pool and applies embedded schema migrations.
func Connect(ctx context.Context, dsn string) (*Postgres, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("storage: connect: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("storage: ping: %w", err)
	}
	pg := &Postgres{pool: pool}
	if err := pg.migrate(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return pg, nil
}

func (p *Postgres) migrate(ctx context.Context) error {
	entries, err := migrationFS.ReadDir("migrations_sql")
	if err != nil {
		return fmt.Errorf("storage: read migrations: %w", err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		sqlBytes, err := migrationFS.ReadFile("migrations_sql/" + e.Name())
		if err != nil {
			return fmt.Errorf("storage: read migration %s: %w", e.Name(), err)
		}
		if _, err := p.pool.Exec(ctx, string(sqlBytes)); err != nil {
			return fmt.Errorf("storage: apply migration %s: %w", e.Name(), err)
		}
	}
	return nil
}

// Close releases the connection pool.
func (p *Postgres) Close() {
	p.pool.Close()
}
