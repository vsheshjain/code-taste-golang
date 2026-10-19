// Package listsvctest provides fakes for listsvc's ports, in the style of
// net/http/httptest: an in-memory Store with real rollback, and a contract
// suite that every Store implementation must pass. Import it from _test.go
// files only.
package listsvctest

import (
	"context"
	"maps"
	"slices"
	"sync"

	"github.com/vsheshjain/code-taste-golang/example/internal/domain/listdom"
	"github.com/vsheshjain/code-taste-golang/example/internal/service/listsvc"
)

// Compile-time proof that Store satisfies the port.
var _ listsvc.UnitOfWork = (*Store)(nil)

// Store is an in-memory UnitOfWork. The zero value is ready to use.
//
// ponytail: one global lock serialises all transactions; fine for tests.
type Store struct {
	mu    sync.Mutex
	lists map[listdom.ID]listdom.List
}

// Do runs fn against a copy of the data and swaps it in only if fn
// returns nil, so a failed use case leaves no partial writes.
func (s *Store) Do(ctx context.Context, fn func(listsvc.Repos) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx := &lists{data: maps.Clone(s.lists)}
	if tx.data == nil {
		tx.data = map[listdom.ID]listdom.List{}
	}
	if err := fn(listsvc.Repos{Lists: tx}); err != nil {
		return err // rollback: drop the copy
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.lists = tx.data // commit
	return nil
}

type lists struct {
	data map[listdom.ID]listdom.List
}

func (r *lists) Get(_ context.Context, id listdom.ID) (*listdom.List, error) {
	l, ok := r.data[id]
	if !ok {
		return nil, listdom.ErrNotFound
	}
	l.Items = slices.Clone(l.Items) // callers must not alias stored state
	return &l, nil
}

func (r *lists) Save(_ context.Context, l *listdom.List) error {
	c := *l
	c.Items = slices.Clone(l.Items)
	r.data[l.ID] = c
	return nil
}
