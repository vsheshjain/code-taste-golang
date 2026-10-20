package listsvc

import (
	"context"

	"github.com/vsheshjain/code-taste-golang/example/internal/domain/listdom"
)

// This file is the service's ports: everything it needs from the outside
// world, declared here by the consumer. Implementations live in repo/ and
// gateway/; fakes live in listsvctest.

// Store is the repository port for the List aggregate.
// Get returns listdom.ErrNotFound when the list doesn't exist.
type Store interface {
	Get(ctx context.Context, id listdom.ID) (*listdom.List, error)
	Save(ctx context.Context, l *listdom.List) error
}

// Repos are the repositories that share one transaction.
type Repos struct {
	Lists Store
}

// UnitOfWork runs fn inside one transaction. Returning nil commits;
// returning an error rolls back.
type UnitOfWork interface {
	Do(ctx context.Context, fn func(Repos) error) error
}
