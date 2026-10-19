# Unit of Work (UoW)

← [Back to index](./README.md)

## One-line definition

A Unit of Work is **the thing that owns a transaction**: it groups the changes of one use case into a single commit-or-rollback.

If Repository is "the seam to storage", Unit of Work is "the transaction boundary around using that seam." It's how you say *"these changes either all happen or none do."*

## The problem it solves

A use case that touches more than one aggregate:

```go
func (s *Service) ShareList(ctx context.Context, id listdom.ID, email string) error {
	l, err := s.lists.Get(ctx, id)
	if err != nil {
		return err
	}
	u, err := s.users.ByEmail(ctx, email)
	if err != nil {
		return err
	}
	l.ShareWith(u.ID)
	u.AddSharedList(id)

	if err := s.lists.Save(ctx, l); err != nil { // commits?
		return err
	}
	return s.users.Save(ctx, u) // fails: the list is already saved
}
```

Without a UoW:

1. **Partial writes.** If saving the user fails, the list is already shared. The database is inconsistent and nothing tells you.
2. **Nobody owns the commit.** Each repository runs its own statement on `*sql.DB` and auto-commits, or you thread a `*sql.Tx` through every service method by hand.
3. **Tests can't check atomicity.** There's no single place to assert "nothing was written."

## What it looks like in Go

Python's UoW is a context manager: `with uow:` plus an explicit `uow.commit()`, and `__exit__` rolls back otherwise. Go has no `with`, but it has closures and `defer`, and the idiomatic equivalent is **a function that runs a callback inside a transaction**:

```go
// internal/service/listsvc/ports.go
type Repos struct {
	Lists Store
	Users Users // listsvc's own small view of users, declared in this same file
}

// UnitOfWork runs fn inside one transaction. Returning nil commits;
// returning an error (or panicking) rolls back.
type UnitOfWork interface {
	Do(ctx context.Context, fn func(Repos) error) error
}
```

The service:

```go
func (s *Service) ShareList(ctx context.Context, id listdom.ID, email string) error {
	return s.uow.Do(ctx, func(r Repos) error {
		l, err := r.Lists.Get(ctx, id)
		if err != nil {
			return err
		}
		u, err := r.Users.ByEmail(ctx, email)
		if err != nil {
			return err
		}
		l.ShareWith(u.ID)
		u.AddSharedList(id)
		if err := r.Lists.Save(ctx, l); err != nil {
			return err
		}
		return r.Users.Save(ctx, u)
	})
}
```

Three things to notice:

1. **Repositories hang off the transaction.** `r.Lists` and `r.Users` are built for this transaction and handed to the callback. The service never holds a repository outside `Do`.
2. **The commit is the `return nil`.** It's still explicit: you can see in the code exactly which path commits. Python makes you call `commit()` because `__exit__` can't see your intent; in Go, the callback's return value *is* the intent, and it's checked by the compiler (you can't forget to return).
3. **The service takes a `UnitOfWork`, not repositories.** Everything that touches storage goes through it.
4. **Each service declares its own `Repos` and `UnitOfWork`** in its `ports.go`, listing only the repositories its use cases touch. `emrsvc.Repos` might hold `Encounters` and `Patients`; `listsvc.Repos` holds `Lists` and `Users`. There is no shared, app-wide `Repos` (see below for why).

## The database/sql version

Repositories live one per domain under `repo/` (`listrepo`, `userrepo`, `patientrepo`). A transaction often spans several of them, so a small `repo/pgtx` package owns the `*sql.Tx` and builds each service's unit of work from it:

```go
// internal/repo/pgtx/pgtx.go
package pgtx

type DB struct {
	db *sql.DB
}

func New(db *sql.DB) *DB { return &DB{db: db} }

// withTx is the only place in the codebase that begins, rolls back and commits.
func (d *DB) withTx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback() // no-op after a successful Commit; runs on error and on panic

	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// Lists is listsvc's unit of work.
func (d *DB) Lists() ListsUoW { return ListsUoW{d} }

type ListsUoW struct{ d *DB }

var _ listsvc.UnitOfWork = ListsUoW{}

func (u ListsUoW) Do(ctx context.Context, fn func(listsvc.Repos) error) error {
	return u.d.withTx(ctx, func(tx *sql.Tx) error {
		return fn(listsvc.Repos{Lists: listrepo.New(tx), Users: userrepo.New(tx)})
	})
}

// EMR is emrsvc's unit of work: encounters and patients in one transaction.
func (d *DB) EMR() EMRUoW { return EMRUoW{d} }

type EMRUoW struct{ d *DB }

func (u EMRUoW) Do(ctx context.Context, fn func(emrsvc.Repos) error) error {
	return u.d.withTx(ctx, func(tx *sql.Tx) error {
		return fn(emrsvc.Repos{Encounters: emrrepo.New(tx), Patients: patientrepo.New(tx)})
	})
}
```

`main` wires it: `listsvc.New(pg.Lists(), …)`, `emrsvc.New(pg.EMR(), …)`.

Why one small `UoW` type per service instead of one shared type? A Go type can have only one `Do` method, and each service's `Do` takes a different `Repos`. And a single app-wide `Repos` would have to live in a package that every service imports while it imports every service's interfaces — an import cycle. Per-service types over one shared `withTx` keep the transaction logic in one place and the ports where they belong.

`defer tx.Rollback()` is the `__exit__`. It runs whatever happens: early return, error, panic. After a successful `Commit` it returns `sql.ErrTxDone`, which we ignore. This is the standard `database/sql` idiom and it's why the closure form fits Go so well: the adapter owns begin, rollback and commit in eight lines, and no service can get the order wrong.

Each repository's constructor takes a small `Querier` interface that both `*sql.DB` and `*sql.Tx` satisfy, so the same `listrepo.Lists` works inside or outside a transaction (see [repository.md](./repository.md)).

## The in-memory version

The fake in [listsvctest/store.go](../../../../example/internal/service/listsvc/listsvctest/store.go) gives real rollback semantics without a database: run the callback against a copy, swap it in only on success.

```go
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
```

One global mutex serialises every transaction. That's a deliberate ceiling, marked in the code, and fine for tests.

The example's real adapter, [listrepo.File](../../../../example/internal/repo/listrepo/listrepo.go), does the same thing against a JSON file: load, run the callback, and only on `nil` write a temp file and rename it into place, so a failed use case or a crash never leaves a half-written file.

## Why a closure, not Begin/Commit on the interface

You could expose `Begin(ctx) (Tx, error)` with `Commit` and `Rollback` methods and let the service drive it. Every service then has to write the `defer tx.Rollback()` dance correctly, every time. The closure puts that dance in exactly one place, the adapter, and makes "forgot to roll back" and "forgot to commit" unrepresentable.

## The alternative: transaction in the context

Some codebases store the `*sql.Tx` in `context.Context` and have repositories pull it out:

```go
func (r *Lists) Get(ctx context.Context, id listdom.ID) (*listdom.List, error) {
	q := txFromContext(ctx, r.db) // tx if present, else db
	...
}
```

It's popular because services can call repositories without a `Repos` argument. We avoid it: it's hidden control flow. Whether a query is transactional depends on what some caller put in a `context.Value`, which the type system can't see and a reader can't find without tracing every caller. The [context package docs](https://pkg.go.dev/context) say to use context values only for request-scoped data that transits processes and APIs, not for passing optional parameters. A transaction is a dependency; pass it as one.

## Real examples

| Use case | Why it needs a UoW |
|---|---|
| Share a list | Updates `List` and `User`. Both or neither. |
| Upgrade a plan | Records the payment ID and changes `User.Plan`. Billing without an upgrade is a support ticket. |
| Send a chat message | Appends a turn and increments the monthly usage counter. |
| Add an item to a list | One aggregate. Not strictly needed, but the example uses `Do` anyway so every use case has the same shape. |

If a use case touches more than one aggregate, or you care about all-or-nothing, you want a UoW. For single-aggregate writes, using it anyway keeps every service method looking the same.

## What's in / what's out

**In the UoW:**
- Begin, commit, rollback
- Building the repositories that share the transaction
- Isolation level and retry-on-serialisation-failure, if you need them

**Out of the UoW:**
- Business rules (domain)
- SQL (repositories)
- Deciding what happens in the use case (service)

## The traps

### Trap 1: network calls inside the transaction

```go
return s.uow.Do(ctx, func(r Repos) error {
	l, err := r.Lists.Get(ctx, id)
	if err != nil {
		return err
	}
	sum, err := s.summarizer.Summarize(ctx, SummaryRequest{Text: l.AsText()}) // 5 s with a tx open
	if err != nil {
		return err
	}
	l.AttachSummary(sum.Text)
	return r.Lists.Save(ctx, l)
})
```

The transaction holds a pooled connection and row locks for as long as the LLM takes. Under load the pool drains and everything stalls. Do external I/O outside:

```go
var text string
if err := s.uow.Do(ctx, func(r Repos) error {
	l, err := r.Lists.Get(ctx, id)
	if err != nil {
		return err
	}
	text = l.AsText()
	return nil
}); err != nil {
	return err
}

sum, err := s.summarizer.Summarize(ctx, SummaryRequest{Text: text})
if err != nil {
	return err
}

return s.uow.Do(ctx, func(r Repos) error {
	l, err := r.Lists.Get(ctx, id) // re-load; check a version if concurrent edits matter
	if err != nil {
		return err
	}
	l.AttachSummary(sum.Text)
	return r.Lists.Save(ctx, l)
})
```

Rule of thumb: **a transaction is short and only talks to its own database.**

### Trap 2: side effects that can't roll back

Sending an email or publishing to a queue inside the callback isn't undone when the transaction rolls back. Either send after `Do` returns `nil`, or write an outbox row in the same transaction and have a worker send it.

### Trap 3: the transaction leaks above the service

```go
func (h *handler) share(w http.ResponseWriter, r *http.Request) {
	tx, _ := h.db.BeginTx(r.Context(), nil) // handler managing the transaction
	...
}
```

The handler is transport. The service owns `Do`, so a queue consumer calling the same service gets the same transaction boundary for free.

### Trap 4: holding repositories outside `Do`

```go
var lists listsvc.Store
s.uow.Do(ctx, func(r Repos) error { lists = r.Lists; return nil })
lists.Save(ctx, l) // using a repo bound to a finished transaction
```

In Postgres this fails with `sql: transaction has already been committed or rolled back`. In memory it silently writes to a discarded copy. Repositories from `Repos` live only for the callback.

### Trap 5: nesting `Do`

A service method calling another service method that also calls `Do` opens a second, independent transaction. The outer rollback won't undo the inner commit. This is another reason services don't call services (see [service.md](./service.md)).

### Trap 6: a UoW you don't need

If you'll always use Postgres and you're happy testing services against a real database, `db.BeginTx` in a small helper is enough. The UoW interface earns rent when you want the in-memory fake, or you have several adapters that must share a transaction.

## How to spot it in your code

```sh
grep -rn 'BeginTx\|\.Begin(\|\*sql.Tx' internal/ | grep -v internal/repo/
```

Any hit outside `repo/` is a transaction leaking upward. Then look for service methods that call `Save` on two repositories without a shared `Do`: those are the partial-write bugs.

## The hidden benefit

**Atomicity becomes testable without a database, and the same test pins every adapter.** The example's contract suite, [listsvctest/contract.go](../../../../example/internal/service/listsvc/listsvctest/contract.go), includes this case:

```go
t.Run("error rolls back", func(t *testing.T) {
	uow := newUoW(t)
	boom := errors.New("boom")
	err := uow.Do(ctx, func(r listsvc.Repos) error {
		if err := r.Lists.Save(ctx, &listdom.List{ID: "l1", Name: "A"}); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want boom", err)
	}
	err = uow.Do(ctx, func(r listsvc.Repos) error {
		_, err := r.Lists.Get(ctx, "l1")
		return err
	})
	if !errors.Is(err, listdom.ErrNotFound) {
		t.Fatalf("after rollback: err = %v, want ErrNotFound", err)
	}
})
```

The fake runs `listsvctest.TestUnitOfWork` against itself in microseconds. The real adapter runs the *same function* in [its own test](../../../../example/internal/repo/listrepo/listrepo_test.go); a Postgres `pgtx` would do the same in the integration suite. If any implementation commits on error, a test fails.

## Self-check

1. What does a UoW give you that a repository alone doesn't?
2. What plays the role of Python's `__exit__` in the `database/sql` version?
3. Python requires an explicit `uow.commit()`. Where is the commit decision in the Go version, and why is it still explicit?
4. Why is calling an LLM inside the `Do` callback a problem?
5. Why do we avoid putting the `*sql.Tx` in the context?
6. What happens if you keep `r.Lists` and use it after `Do` returns?

## Things to steal

- **`Do(ctx, func(Repos) error) error`.** Nil commits, error rolls back.
- **One `UnitOfWork` per service, declared in its `ports.go`,** all built in `repo/pgtx` over one shared `withTx`.
- **`defer tx.Rollback()` right after `BeginTx`.** Always.
- **Repositories are built per transaction and live only inside the callback.**
- **One `Do` per use case, owned by the service,** never by the handler.
- **No network calls and no irreversible side effects inside the transaction.**
- **Pass the transaction explicitly,** not through `context.Value`.
- **The fake UoW makes rollback a unit test; the contract suite makes every adapter pass the same one.**
