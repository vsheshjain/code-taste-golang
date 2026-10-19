// Package memory is an in-memory adapter for the app ports. It backs the
// service tests and local runs; a postgres package would sit beside it with
// the same shape.
package memory

import (
	"context"
	"maps"
	"slices"
	"sync"

	"github.com/vsheshjain/code-taste-golang/example/internal/app"
	"github.com/vsheshjain/code-taste-golang/example/internal/list"
)

// Compile-time proof that Store satisfies the port.
var _ app.UnitOfWork = (*Store)(nil)

// Store holds every list. The zero value is ready to use.
//
// ponytail: one global lock serialises all transactions; fine for tests and
// demos, a real database is the upgrade path.
type Store struct {
	mu    sync.Mutex
	lists map[list.ID]list.List
}

// Do runs fn against a copy of the data and swaps it in only if fn
// returns nil, so a failed use case leaves no partial writes.
func (s *Store) Do(ctx context.Context, fn func(app.Repos) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx := &lists{data: maps.Clone(s.lists)}
	if tx.data == nil {
		tx.data = map[list.ID]list.List{}
	}
	if err := fn(app.Repos{Lists: tx}); err != nil {
		return err // rollback: drop the copy
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.lists = tx.data // commit
	return nil
}

type lists struct {
	data map[list.ID]list.List
}

func (r *lists) Get(_ context.Context, id list.ID) (*list.List, error) {
	l, ok := r.data[id]
	if !ok {
		return nil, list.ErrNotFound
	}
	l.Items = slices.Clone(l.Items) // callers must not alias stored state
	return &l, nil
}

func (r *lists) Save(_ context.Context, l *list.List) error {
	c := *l
	c.Items = slices.Clone(l.Items)
	r.data[l.ID] = c
	return nil
}
