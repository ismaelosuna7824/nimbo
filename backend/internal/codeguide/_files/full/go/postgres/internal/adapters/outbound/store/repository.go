// Package store holds the driven adapters of ItemRepository.
package store

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"example.com/api/internal/application"
	"example.com/api/internal/domain"
)

type Postgres struct{ pool *pgxpool.Pool }

// New builds the repository from the env the composition root passes in.
func New(ctx context.Context, getenv func(string) string) (application.ItemRepository, error) {
	url := getenv("DATABASE_URL")
	if url == "" {
		return nil, errors.New("DATABASE_URL is not set")
	}
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		return nil, err
	}
	_, err = pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS items (id text PRIMARY KEY, name text NOT NULL, created_at timestamptz NOT NULL)`)
	return &Postgres{pool}, err
}

func (p *Postgres) Save(ctx context.Context, item domain.Item) error {
	tag, err := p.pool.Exec(ctx, `INSERT INTO items (id, name, created_at) VALUES ($1, $2, $3) ON CONFLICT (id) DO NOTHING`,
		item.ID, item.Name.String(), item.CreatedAt)
	if err == nil && tag.RowsAffected() == 0 {
		return domain.ErrConflict
	}
	return err
}

func (p *Postgres) Get(ctx context.Context, id string) (domain.Item, error) {
	var name string
	item := domain.Item{ID: id}
	err := p.pool.QueryRow(ctx, `SELECT name, created_at FROM items WHERE id = $1`, id).Scan(&name, &item.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Item{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Item{}, err
	}
	item.Name, err = domain.NewItemName(name)
	return item, err
}
