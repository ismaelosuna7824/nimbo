package application_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"example.com/api/internal/adapters/outbound/store"
	"example.com/api/internal/application"
	"example.com/api/internal/domain"
)

func TestCreateThenGet(t *testing.T) {
	u := application.Items{Repo: store.NewMemory(), NewID: func() string { return "id-1" }, Now: func() time.Time { return time.Unix(0, 0) }}
	ctx := context.Background()
	created, err := u.Create(ctx, "pen")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := u.Get(ctx, "id-1"); err != nil || got != created {
		t.Fatalf("Get = %+v, %v; want %+v", got, err, created)
	}
	if _, err := u.Create(ctx, "pen"); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("duplicate id: err = %v, want ErrConflict", err)
	}
}
