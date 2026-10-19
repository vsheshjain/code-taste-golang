# Repository

← [Back to index](./README.md)

## One-line definition

A repository is **the one place that knows how to load and save your domain types**, and the only code that talks to the database.

Think of it as a *collection-shaped interface over storage*. Code that uses it can't tell whether the data lives in Postgres, SQLite, or a map.

## The problem it solves

Without a repository, `db.QueryContext` and `rows.Scan` end up everywhere: in services, in HTTP handlers, in helpers. Three pains follow:

1. **Tests are painful.** To test a use case you need a real database or `sqlmock` expectations that mirror every query string.
2. **Swapping storage is a rewrite.** Moving from SQLite to Postgres, or from `database/sql` to pgx, touches every file.
3. **The domain gets polluted.** `db:"..."` tags and `sql.NullString` fields creep onto `listdom.List`.

A repository fixes all three by being **the single seam** between domain and storage.

## What it looks like

In Go the interface is declared by the **consumer**, the package that uses it, not by the package that implements it. The [Code Review Comments](https://go.dev/wiki/CodeReviewComments#interfaces) wiki is direct about this: "Go interfaces generally belong in the package that uses values of the interface type, not the package that implements those values." So the port lives in the service that uses it, in a file called `ports.go` so it's easy to find ([internal/service/listsvc/ports.go](../../../../example/internal/service/listsvc/ports.go)):

```go
// internal/service/listsvc/ports.go
type Store interface {
	Get(ctx context.Context, id listdom.ID) (*listdom.List, error)
	Save(ctx context.Context, l *listdom.List) error
}
```

Two methods. That's all the use cases need today, so that's all the interface has. Add `ListForOwner` the day a use case calls it, not before. Small interfaces are easy to fake and easy to satisfy (`io.Reader` has one method).

The implementations live under `internal/repo/`, one package per domain (`listrepo`, `patientrepo`), and satisfy the interface implicitly. No `implements` keyword, no import of the interface required.

A Postgres adapter over `database/sql`:

```go
// internal/repo/listrepo/postgres.go
package listrepo

type Lists struct {
	q Querier
}

// Querier is satisfied by both *sql.DB and *sql.Tx, so the same repo works
// inside or outside a transaction (see unit-of-work.md).
type Querier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func New(q Querier) *Lists { return &Lists{q: q} }

func (r *Lists) Get(ctx context.Context, id listdom.ID) (*listdom.List, error) {
	l := &listdom.List{ID: id}
	err := r.q.QueryRowContext(ctx,
		`SELECT owner, name, slug FROM lists WHERE id = $1`, id,
	).Scan(&l.Owner, &l.Name, &l.Slug)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, listdom.ErrNotFound // translate at the seam
	}
	if err != nil {
		return nil, fmt.Errorf("get list %s: %w", id, err)
	}

	rows, err := r.q.QueryContext(ctx,
		`SELECT url, title FROM items WHERE list_id = $1 ORDER BY position`, id)
	if err != nil {
		return nil, fmt.Errorf("get items for %s: %w", id, err)
	}
	defer rows.Close()
	for rows.Next() {
		var it listdom.Item
		if err := rows.Scan(&it.URL, &it.Title); err != nil {
			return nil, err
		}
		l.Items = append(l.Items, it)
	}
	return l, rows.Err()
}
```

The example's real adapter, [internal/repo/listrepo/listrepo.go](../../../../example/internal/repo/listrepo/listrepo.go), stores lists in a JSON file so it stays stdlib-only. Same shape: an unexported row type, translated to the domain at the seam:

```go
func (r *lists) Get(_ context.Context, id listdom.ID) (*listdom.List, error) {
	row, ok := r.rows[string(id)]
	if !ok {
		return nil, listdom.ErrNotFound // translate at the seam
	}
	l := &listdom.List{ID: listdom.ID(row.ID), Owner: row.Owner, Name: row.Name, Slug: row.Slug}
	for _, it := range row.Items {
		l.Items = append(l.Items, listdom.Item(it))
	}
	return l, nil
}
```

And the fake, which lives beside the port in [listsvctest](../../../../example/internal/service/listsvc/listsvctest/store.go), not under `repo/`. It implements `listsvc`'s interface, so it belongs to `listsvc`, the way `httptest` belongs to `net/http`:

```go
func (r *lists) Get(_ context.Context, id listdom.ID) (*listdom.List, error) {
	l, ok := r.data[id]
	if !ok {
		return nil, listdom.ErrNotFound
	}
	l.Items = slices.Clone(l.Items) // callers must not alias stored state
	return &l, nil
}
```

Things to notice:

- **`context.Context` is the first parameter** of every method that does I/O. It carries cancellation and deadlines from the HTTP request down to the query. It's never stored in the struct ([context and structs](https://go.dev/blog/context-and-structs)).
- **It returns `*listdom.List`, never `*sql.Rows`**, never a row struct. The caller gets a domain value.
- **Not-found is the domain's error.** `sql.ErrNoRows` stops at the adapter. The service matches `listdom.ErrNotFound` with `errors.Is` and never imports `database/sql`.
- **`defer rows.Close()` and `return rows.Err()`.** Forgetting either leaks connections or swallows errors silently.
- **Unexpected errors are wrapped with `%w`** so callers can still `errors.Is` them while the message carries context ([Go 1.13 errors](https://go.dev/blog/go1.13-errors)).

## Real examples

| App | Real swap-case | Justified? |
|---|---|---|
| Reading-list example | JSON file (`listrepo.File`) in the example, Postgres in prod, `listsvctest.Store` in tests | Yes. Fast service tests alone pay for it |
| Service that will move from SQLite to Postgres next quarter | The swap is scheduled | Yes |
| Service that will only ever use Postgres, with integration tests against a real DB | No second impl | Maybe not. Call `*sql.DB` from the service and test with a containerised DB |
| CLI with no durable state | Nothing to store | N/A |

The honest test: **can you name a real second implementation?** For most apps it's the in-memory one for tests. That alone usually justifies the interface.

## What's in / what's out

**In the repository:**
- Loading aggregates by ID or by a domain question (`ListsForOwner`, `FindBySlug`)
- Saving and deleting aggregates
- Mapping between rows and domain structs (`Scan` into `listdom.List`, nullable columns into zero values)
- Translating driver errors into domain errors (`sql.ErrNoRows` → `listdom.ErrNotFound`, unique-violation → `listdom.ErrDuplicate...`)

**Out of the repository:**
- Business rules (on the domain type)
- Beginning and committing transactions (the [Unit of Work](./unit-of-work.md))
- HTTP concerns
- Caching, retries, metrics. If you need them, wrap the repository in another type that satisfies the same interface (a decorator), don't bury them inside.

## The traps

### Trap 1: a thin SQL wrapper

```go
type Lists interface {
	Query(ctx context.Context, sql string, args ...any) (*sql.Rows, error)
	Exec(ctx context.Context, sql string, args ...any) error
}
```

That's `*sql.DB` with a different name. Repository methods should answer domain questions: `ListsForOwner(ctx, owner)`, not `Query(ctx, "SELECT ...")`.

### Trap 2: leaking storage types

If `Get` returns a `ListRow` with `sql.NullString` fields and `db` tags, or worse a `*sql.Rows` the caller has to close, nothing is decoupled. The service now knows about nullability and connection lifetimes.

Two options:
- **Easy:** the domain struct is what you scan into, and there's no separate row type. Fine while the tables map one-to-one to the aggregate.
- **Strict:** an unexported row type in the repo package and a translation function, like `listRow` in the example's `listrepo`. Worth it once columns and fields drift apart.

Either way, the row type never leaves the adapter package. Keeping it unexported makes that a compile error, not a code-review comment.

### Trap 3: one repository per table

A repository is **per aggregate**. `Item`s only exist inside a `List`, so there's one `Lists` repository that loads and saves a list with its items, even though that's two tables. A separate `Items` repository invites code that edits items without going through `List.AddItem`, and the rules get bypassed.

### Trap 4: the interface in the implementing package

```go
// internal/repo/listrepo/postgres.go
type ListRepository interface { ... }      // declared here
type listRepository struct { ... }          // unexported impl
func NewListRepository(db *sql.DB) ListRepository { ... } // returns the interface
```

This is Java-shaped Go. It forces the service to import the repo to get the interface (now `service` depends on `repo`, the wrong way round), hides the concrete type from callers who might need its extra methods, and pre-commits to an abstraction before anyone asked. Go's convention is the opposite: **accept interfaces, return structs**. `listrepo.New(db)` returns `*listrepo.Lists`; `listsvc` declares the `Store` interface it needs in `ports.go`; `main` passes one to the other.

### Trap 5: a repository when there's no domain

If your "domain type" has no methods, your repository is a CRUD wrapper, and your service just forwards to it, you've built three layers for what one handler function and a `*sql.DB` could do. Wait until there are rules worth protecting.

## How to spot it in your code

```sh
grep -rn '"database/sql"\|pgx' internal/ | grep -v internal/repo/
```

Any hit outside the storage adapter is a leak. The fix is usually mechanical: move the query into a method on the adapter, name the method after the question it answers, and add that method to the consumer's interface.

Also look for `*sql.Tx` or `*sql.DB` parameters on service methods. That's the transaction leaking upward. See [unit-of-work.md](./unit-of-work.md).

## The hidden benefit

**Fast service tests with no database.** The example's service tests build the whole service on `&listsvctest.Store{}` and run in milliseconds:

```go
func TestCreateListAssignsSlug(t *testing.T) {
	ctx := context.Background()
	svc := listsvc.New(&listsvctest.Store{}, func() listdom.ID { return "l1" })

	if _, err := svc.CreateList(ctx, "u1", "My Stuff"); err != nil {
		t.Fatal(err)
	}
	got, err := svc.GetList(ctx, "l1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Slug != "my-stuff" {
		t.Errorf("slug = %q, want %q", got.Slug, "my-stuff")
	}
}
```

No Docker, no migrations, no `sqlmock` strings. The real adapter gets its own smaller set of integration tests. See [testing.md](./testing.md).

**The contract suite keeps the fake honest.** A fake is only useful if it behaves like the real thing. [listsvctest/contract.go](../../../../example/internal/service/listsvc/listsvctest/contract.go) exports `TestUnitOfWork(t, newUoW)`: one set of assertions (missing list is `ErrNotFound`, committed saves are readable, an error rolls back). The fake runs it in its own test, and every real adapter runs the same function in *its* test:

```go
// internal/repo/listrepo/listrepo_test.go
func TestFile(t *testing.T) {
	listsvctest.TestUnitOfWork(t, func(t *testing.T) listsvc.UnitOfWork {
		return listrepo.New(filepath.Join(t.TempDir(), "lists.json"))
	})
}
```

If the fake and Postgres ever disagree about behaviour, one of them fails. The standard library does the same with [`testing/fstest.TestFS`](https://pkg.go.dev/testing/fstest#TestFS).

## Self-check

1. Which package declares the `Store` interface, and why that one and not `listrepo`?
2. Where does `sql.ErrNoRows` stop, and what does the caller see instead?
3. Why is `Query(ctx, sql string)` on a repository a smell? Give a better method name.
4. Why does the fake's `Get` in `listsvctest` clone `Items` before returning?
5. Why does the fake live in `listsvctest` rather than under `repo/`, and what stops it drifting from the real adapter?
6. Your aggregate has no methods. Does a repository help? Why or why not?

## Things to steal

- **The consumer owns the interface.** `listsvc.Store` in `ports.go`, not `listrepo.ListRepository`.
- **Accept interfaces, return structs.** `listrepo.New` returns `*listrepo.Lists`.
- **Interfaces as small as today's use cases.** Grow them when a caller needs more.
- **Domain types out, driver errors translated.** Nothing from `database/sql` crosses the seam.
- **`ctx` first, `rows.Close` deferred, `rows.Err` checked.** Every time.
- **One repository per aggregate.**
- **The in-memory fake is the killer feature.** Keep it in `<svc>test` beside the port, and run one contract suite against it and the real repo.
