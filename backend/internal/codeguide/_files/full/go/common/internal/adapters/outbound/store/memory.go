package store

import (
	"context"
	"sync"

	"example.com/api/internal/domain"
)

// Memory is the in-memory adapter for tests and local runs.
type Memory struct {
	mu   sync.Mutex
	rows map[string]domain.Item
}

func NewMemory() *Memory { return &Memory{rows: map[string]domain.Item{}} }

func (m *Memory) Save(_ context.Context, item domain.Item) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.rows[item.ID]; ok {
		return domain.ErrConflict
	}
	m.rows[item.ID] = item
	return nil
}

func (m *Memory) Get(_ context.Context, id string) (domain.Item, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if item, ok := m.rows[id]; ok {
		return item, nil
	}
	return domain.Item{}, domain.ErrNotFound
}
