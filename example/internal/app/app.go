// Package app holds the use cases (application services) and the
// interfaces they need from the outside world (the ports). Interfaces live
// here, with their consumer, not next to their implementations.
package app

import (
	"context"
	"fmt"

	"github.com/vsheshjain/code-taste-golang/example/internal/list"
)

// Lists is the repository port for the List aggregate.
// Get returns list.ErrNotFound when the list doesn't exist.
type Lists interface {
	Get(ctx context.Context, id list.ID) (*list.List, error)
	Save(ctx context.Context, l *list.List) error
}

// Repos are the repositories that share one transaction.
type Repos struct {
	Lists Lists
}

// UnitOfWork runs fn inside one transaction. Returning nil commits;
// returning an error (or panicking) rolls back.
type UnitOfWork interface {
	Do(ctx context.Context, fn func(Repos) error) error
}

// Service runs the list use cases. Build it with New in cmd/; never
// construct adapters in here.
type Service struct {
	uow   UnitOfWork
	newID func() list.ID
}

// New wires a Service. newID is injected so tests get stable IDs.
func New(uow UnitOfWork, newID func() list.ID) *Service {
	return &Service{uow: uow, newID: newID}
}

// CreateList creates an empty list for owner.
func (s *Service) CreateList(ctx context.Context, owner, name string) (*list.List, error) {
	l, err := list.New(s.newID(), owner, name)
	if err != nil {
		return nil, err
	}
	err = s.uow.Do(ctx, func(r Repos) error {
		return r.Lists.Save(ctx, l)
	})
	if err != nil {
		return nil, fmt.Errorf("create list: %w", err)
	}
	return l, nil
}

// AddItem appends an item to a list: load, delegate, save.
func (s *Service) AddItem(ctx context.Context, id list.ID, it list.Item) error {
	return s.uow.Do(ctx, func(r Repos) error {
		l, err := r.Lists.Get(ctx, id)
		if err != nil {
			return fmt.Errorf("add item to %s: %w", id, err)
		}
		if err := l.AddItem(it); err != nil {
			return err
		}
		return r.Lists.Save(ctx, l)
	})
}

// GetList returns one list. A one-line pass-through, kept so every
// entrypoint goes through the service.
func (s *Service) GetList(ctx context.Context, id list.ID) (*list.List, error) {
	var l *list.List
	err := s.uow.Do(ctx, func(r Repos) (err error) {
		l, err = r.Lists.Get(ctx, id)
		return err
	})
	return l, err
}
