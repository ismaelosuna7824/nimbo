// Package application holds the use cases and the ports they own.
package application

import (
	"context"
	"time"

	"example.com/api/internal/domain"
)

// ItemRepository is a driven port. Save returns domain.ErrConflict when the
// id exists (idempotent create).
type ItemRepository interface {
	Save(ctx context.Context, item domain.Item) error
	Get(ctx context.Context, id string) (domain.Item, error) // domain.ErrNotFound when missing
}

// ItemView is the plain data that crosses the boundary.
type ItemView struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"createdAt"`
}

func view(i domain.Item) ItemView { return ItemView{i.ID, i.Name.String(), i.CreatedAt} }

type Items struct {
	Repo  ItemRepository
	NewID func() string
	Now   func() time.Time
}

func (u Items) Create(ctx context.Context, name string) (ItemView, error) {
	n, err := domain.NewItemName(name)
	if err != nil {
		return ItemView{}, err
	}
	item := domain.Item{ID: u.NewID(), Name: n, CreatedAt: u.Now().UTC()}
	if err := u.Repo.Save(ctx, item); err != nil {
		return ItemView{}, err
	}
	return view(item), nil
}

func (u Items) Get(ctx context.Context, id string) (ItemView, error) {
	item, err := u.Repo.Get(ctx, id)
	if err != nil {
		return ItemView{}, err
	}
	return view(item), nil
}
