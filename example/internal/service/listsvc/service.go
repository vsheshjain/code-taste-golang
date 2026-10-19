// Package listsvc holds the list use cases (application services) and, in
// ports.go, the interfaces they need from the outside world.
package listsvc

import (
	"context"
	"fmt"

	"github.com/vsheshjain/code-taste-golang/example/internal/domain/listdom"
)

// Service runs the list use cases. Build it with New in cmd/; never
// construct adapters in here.
type Service struct {
	uow   UnitOfWork
	newID func() listdom.ID
}

// New wires a Service. newID is injected so tests get stable IDs.
func New(uow UnitOfWork, newID func() listdom.ID) *Service {
	return &Service{uow: uow, newID: newID}
}

// CreateList creates an empty list for owner.
func (s *Service) CreateList(ctx context.Context, owner, name string) (*listdom.List, error) {
	l, err := listdom.New(s.newID(), owner, name)
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
func (s *Service) AddItem(ctx context.Context, id listdom.ID, it listdom.Item) error {
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
func (s *Service) GetList(ctx context.Context, id listdom.ID) (*listdom.List, error) {
	var l *listdom.List
	err := s.uow.Do(ctx, func(r Repos) (err error) {
		l, err = r.Lists.Get(ctx, id)
		return err
	})
	return l, err
}
