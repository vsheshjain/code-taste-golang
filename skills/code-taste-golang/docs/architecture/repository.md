# Repository

← [Back to index](./README.md)

## One-line definition

A repository is **the one place that knows how to load and save your domain types**, and the only code that talks to the database.

Think of it as a *collection-shaped interface over storage*. Code that uses it can't tell whether the data lives in Postgres, SQLite, or a map.

## The problem it solves

Without a repository, `db.QueryContext` and `rows.Scan` end up everywhere: in services, in HTTP handlers, in helpers. Three pains follow:

1. **Tests are painful.** To test a use case you need a real database or `sqlmock` expectations that mirror every query string.
2. **Swapping storage is a rewrite.** Moving from SQLite to Postgres, or from `database/sql` to pgx, touches every file.
3. **The domain gets polluted.** `db:"..."` tags and `sql.NullString` fields creep onto `list.List`.

A repository fixes all three by being **the single seam** between domain and storage.

## What it looks like

In Go the interface is declared by the **consumer**, the package that uses it, not by the package that implements it. The [Code Review Comments](https://go.dev/wiki/CodeReviewComments#interfaces) wiki is direct about this: "Go interfaces generally belong in the package that uses values of the interface type, not the package that implements those values." So the port lives in `internal/app`, next to the services:

```go
// internal/app/app.go
type Lists interface {
	Get(ctx context.Context, id list.ID) (*list.List, error)
	Save(ctx context.Context, l *list.List) error
}
```

Two methods. That's all the use cases need today, so that's all the interface has. Add `ListForOwner` the day a use case calls it, not before. Small interfaces are easy to fake and easy to satisfy (`io.Reader` has one method).

The implementations live in packages named for their technology, and satisfy the interface implicitly. No `implements` keyword, no import of the interface required.

A Postgres adapter over `database/sql`:

```go
// internal/postgres/lists.go
package postgres

type Lists struct {
	q querier // *sql.DB or *sql.Tx
}

type querier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func (r *Lists) Get(ctx context.Context, id list.ID) (*list.List, error) {
	l := &list.List{ID: id}
	err := r.q.QueryRowContext(ctx,
		`SELECT owner, name, slug FROM lists WHERE id = $1`, id,
	).Scan(&l.Owner, &l.Name, &l.Slug)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, list.ErrNotFound // translate at the seam
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
		var it list.Item
		if err := rows.Scan(&it.URL, &it.Title); err != nil {
			return nil, err
		}
		l.Items = append(l.Items, it)
	}
	return l, rows.Err()
}
```

And the in-memory one from the example, [internal/memory/memory.go](../../../../example/internal/memory/memory.go):

```go
func (r *lists) Get(_ context.Context, id list.ID) (*list.List, error) {
	l, ok := r.data[id]
	if !ok {
		return nil, list.ErrNotFound
	}
	l.Items = slices.Clone(l.Items) // callers must not alias stored state
	return &l, nil
}
```

Things to notice:

- **`context.Context` is the first parameter** of every method that does I/O. It carries cancellation and deadlines from the HTTP request down to the query. It's never stored in the struct ([context and structs](https://go.dev/blog/context-and-structs)).
- **It returns `*list.List`, never `*sql.Rows`**, never a row struct. The caller gets a domain value.
- **Not-found is the domain's error.** `sql.ErrNoRows` stops at the adapter. The service matches `list.ErrNotFound` with `errors.Is` and never imports `database/sql`.
- **`defer rows.Close()` and `return rows.Err()`.** Forgetting either leaks connections or swallows errors silently.
- **Unexpected errors are wrapped with `%w`** so callers can still `errors.Is` them while the message carries context ([Go 1.13 errors](https://go.dev/blog/go1.13-errors)).

## Real examples

| App | Real swap-case | Justified? |
|---|---|---|
| Reading-list example | Postgres in prod, `memory.Store` in tests | Yes. Fast service tests alone pay for it |
| Service that will move from SQLite to Postgres next quarter | The swap is scheduled | Yes |
| Service that will only ever use Postgres, with integration tests against a real DB | No second impl | Maybe not. Call `*sql.DB` from the service and test with a containerised DB |
| CLI with no durable state | Nothing to store | N/A |

The honest test: **can you name a real second implementation?** For most apps it's the in-memory one for tests. That alone usually justifies the interface.

## What's in / what's out

**In the repository:**
- Loading aggregates by ID or by a domain question (`ListsForOwner`, `FindBySlug`)
- Saving and deleting aggregates
- Mapping between rows and domain structs (`Scan` into `list.List`, nullable columns into zero values)
- Translating driver errors into domain errors (`sql.ErrNoRows` → `list.ErrNotFound`, unique-violation → `list.ErrDuplicate...`)

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
- **Strict:** an unexported row type in `postgres` and a `toDomain()` function. Worth it once columns and fields drift apart.

Either way, the row type never leaves the adapter package. Keeping it unexported makes that a compile error, not a code-review comment.

### Trap 3: one repository per table

A repository is **per aggregate**. `Item`s only exist inside a `List`, so there's one `Lists` repository that loads and saves a list with its items, even though that's two tables. A separate `Items` repository invites code that edits items without going through `List.AddItem`, and the rules get bypassed.

### Trap 4: the interface in the implementing package

```go
// internal/postgres/lists.go
type ListRepository interface { ... }      // declared here
type listRepository struct { ... }          // unexported impl
func NewListRepository(db *sql.DB) ListRepository { ... } // returns the interface
```

This is Java-shaped Go. It forces every consumer to import `postgres` to get the interface, hides the concrete type from callers who might need its extra methods, and pre-commits to an abstraction before anyone asked. Go's convention is the opposite: **accept interfaces, return structs**. `postgres.NewLists(db)` returns `*postgres.Lists`; `app` declares the `Lists` interface it needs; `main` passes one to the other.

### Trap 5: a repository when there's no domain

If your "domain type" has no methods, your repository is a CRUD wrapper, and your service just forwards to it, you've built three layers for what one handler function and a `*sql.DB` could do. Wait until there are rules worth protecting.

## How to spot it in your code

```sh
grep -rn '"database/sql"\|pgx' internal/ | grep -v internal/postgres
```

Any hit outside the storage adapter is a leak. The fix is usually mechanical: move the query into a method on the adapter, name the method after the question it answers, and add that method to the consumer's interface.

Also look for `*sql.Tx` or `*sql.DB` parameters on service methods. That's the transaction leaking upward. See [unit-of-work.md](./unit-of-work.md).

## The hidden benefit

**Fast service tests with no database.** The example's service tests build the whole service on `&memory.Store{}` and run in milliseconds:

```go
func TestCreateListAssignsSlug(t *testing.T) {
	ctx := context.Background()
	svc := app.New(&memory.Store{}, func() list.ID { return "l1" })

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

No Docker, no migrations, no `sqlmock` strings. The Postgres adapter gets its own smaller set of integration tests against a real database. See [testing.md](./testing.md).

## Self-check

1. Which package declares the `Lists` interface, and why that one and not `postgres`?
2. Where does `sql.ErrNoRows` stop, and what does the caller see instead?
3. Why is `Query(ctx, sql string)` on a repository a smell? Give a better method name.
4. Why does the example's in-memory `Get` clone `Items` before returning?
5. Your aggregate has no methods. Does a repository help? Why or why not?

## Things to steal

- **The consumer owns the interface.** `app.Lists`, not `postgres.ListRepository`.
- **Accept interfaces, return structs.** `postgres.NewLists` returns `*postgres.Lists`.
- **Interfaces as small as today's use cases.** Grow them when a caller needs more.
- **Domain types out, driver errors translated.** Nothing from `database/sql` crosses the seam.
- **`ctx` first, `rows.Close` deferred, `rows.Err` checked.** Every time.
- **One repository per aggregate.**
- **The in-memory implementation is the killer feature.** It's why most teams keep the pattern.
