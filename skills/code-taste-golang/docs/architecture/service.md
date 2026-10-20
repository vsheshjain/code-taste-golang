# Service (Service Layer)

← [Back to index](./README.md)

## One-line definition

A service is **the method that runs one use case from start to finish**: load the aggregates it needs, ask the domain to do its thing, persist the result.

It's the layer that says *"this is what happens when a user does X."*

## The problem it solves

Without a service layer, use-case logic lands in one of two bad places:

1. **In the HTTP handler.** Now the use case is tangled with JSON decoding, `r.PathValue`, and status codes. You can't call it from a CLI, a queue consumer, or a test without building an `*http.Request`.
2. **On the domain type.** Now `List` loads other aggregates and talks to repositories, and the domain isn't pure anymore.

The service layer is the home for **orchestration that's neither transport nor business rule**. It answers *"where does the workflow live?"*

## What it looks like

In this layout each domain gets its own service package under `internal/service/`: `listsvc`, `patientsvc`, `emrsvc`. Each has two files that matter:

- `ports.go` declares every interface the service needs from outside (repositories, unit of work, external systems) plus the DTOs those interfaces take and return.
- `service.go` holds a `Service` struct with its dependencies, and one method per use case. Dependencies come in through `New`, typed as the interfaces from `ports.go`.

From [internal/service/listsvc/service.go](../../../../example/internal/service/listsvc/service.go):

```go
type Service struct {
	uow   UnitOfWork
	newID func() listdom.ID
}

func New(uow UnitOfWork, newID func() listdom.ID) *Service {
	return &Service{uow: uow, newID: newID}
}

// AddItem appends an item to a list: load, delegate, save.
func (s *Service) AddItem(ctx context.Context, id listdom.ID, it listdom.Item) error {
	return s.uow.Do(ctx, func(r Repos) error {
		l, err := r.Lists.Get(ctx, id)            // 1. load
		if err != nil {
			return fmt.Errorf("add item to %s: %w", id, err)
		}
		if err := l.AddItem(it); err != nil {    // 2. delegate to the domain
			return err
		}
		return r.Lists.Save(ctx, l)              // 3. persist
	})
}
```

Notice what's *not* there:

- No `if len(l.Items) >= 100`. That's a domain rule, it lives in `(*listdom.List).AddItem`.
- No `http.ResponseWriter`, no status codes. That's the handler's job.
- No `tx.Commit()`. The [Unit of Work](./unit-of-work.md) commits when the closure returns `nil`.
- No `listrepo.`, `listsvctest.` or any `gateway/` package anywhere. The service only knows the interfaces in its own `ports.go`.

Also notice `newID func() listdom.ID`. A single-method dependency doesn't need an interface; a function value is the idiomatic Go seam, and it makes tests deterministic. The same goes for clocks: inject `now func() time.Time` rather than calling `time.Now()` inside the use case.

## Functions or a struct?

Python services are often module-level functions that take the repository as an argument. In Go you can do the same:

```go
func AddItem(ctx context.Context, uow UnitOfWork, id listdom.ID, it listdom.Item) error
```

That's fine for a handful of use cases. Once there are several dependencies (UoW, summarizer, clock, logger), every call site repeats them, and a `Service` struct built once in `main` is cleaner. Pick one style per package.

Either way: **one exported method per use case, named for what the user does.** `CreateList`, `AddItem`, `ShareList`. Not `InsertListRow`.

## "Service" vs "domain service"

The word *service* means two things:

| Type | Lives in | Example | Job |
|---|---|---|---|
| **Application service** (this doc) | `internal/service/<name>svc` | `(*Service).AddItem` | Orchestrates a use case across domain and ports |
| **Domain service** | the domain package | `billingdom.Prorate(old, new Plan, at time.Time) Money` | A pure rule that spans several domain values and has no natural single owner |

In Go a domain service is just a plain function in the domain package. No struct, no interface, no dependencies. If it needs a repository, it's not a domain service.

## Real examples

| Use case | Service method | What it orchestrates |
|---|---|---|
| User creates a list | `CreateList(ctx, owner, name)` | `listdom.New` validates and slugs; save via UoW |
| User adds an item | `AddItem(ctx, id, item)` | Load list, `l.AddItem`, save |
| User sends a chat message | `SendMessage(ctx, convID, text)` | Load conversation, call `Completer` port, append turn, save |
| User summarises a list | `Summarize(ctx, id)` | Load list, call `Summarizer` port, save summary |
| User upgrades plan | `Upgrade(ctx, userID, plan)` | Load user, call `Payments` port, `u.Upgrade(plan)`, save |

Each one is **one verb from the user's perspective**. That's the test for "is this a service method?"

## What's in / what's out

**In a service:**
- Opening a unit of work and loading aggregates
- Calling domain methods
- Calling ports for external systems (LLM, payments, email)
- Wrapping errors with use-case context (`fmt.Errorf("add item to %s: %w", id, err)`)
- Authorisation checks that need loaded data ("is this user the owner?")
- Converting primitives into domain values (`listdom.ID(id)`, `listdom.New(...)`)

**Out of a service:**
- Business rules (domain)
- HTTP: decoding, status codes, headers (handler)
- SQL (repository)
- Choosing concrete adapters (`main`)
- Logging every error. Return it wrapped; the edge logs it once.

## The traps

### Trap 1: the fat service

```go
// Wrong: business rules in the service
func (s *Service) AddItem(ctx context.Context, id listdom.ID, it listdom.Item) error {
	return s.uow.Do(ctx, func(r Repos) error {
		l, err := r.Lists.Get(ctx, id)
		if err != nil {
			return err
		}
		if len(l.Items) >= 100 {
			return errors.New("list full")
		}
		for _, x := range l.Items {
			if x.URL == it.URL {
				return errors.New("duplicate")
			}
		}
		l.Items = append(l.Items, it)
		return r.Lists.Save(ctx, l)
	})
}
```

The smell is `if`s about *the business* and `errors.New` created inline. Move both onto the domain type, and the errors become domain sentinels callers can match.

### Trap 2: the pass-through service

```go
func (s *Service) GetList(ctx context.Context, id listdom.ID) (*listdom.List, error) {
	var l *listdom.List
	err := s.uow.Do(ctx, func(r Repos) (err error) {
		l, err = r.Lists.Get(ctx, id)
		return err
	})
	return l, err
}
```

The example keeps this one on purpose so every entrypoint goes through `Service` and the handler never sees a repository. That's defensible. Dropping it and letting the handler read the repository is also defensible for read-heavy apps. Know which one you chose, and be consistent.

### Trap 3: services calling services

If `emrsvc` imports `patientsvc` to call `patientsvc.Service.Get`, you've built a coordination layer on top of your coordination layer, you'll soon have nested units of work, and two services that need each other become an import cycle. The example's [arch_test.go](../../../../example/arch_test.go) fails the build on any service-to-service import.

Instead:

- **A rule** goes down to the domain. `emrdom` may import `patientdom` (one direction only).
- **A workflow step** becomes an unexported function in the same service package.
- **Data owned by another domain** is declared as a small interface in the consuming service's `ports.go`, and the other domain's repo satisfies it:

```go
// internal/service/emrsvc/ports.go
type Patients interface { // emrsvc's own view of patients: just what it needs
	Get(ctx context.Context, id patientdom.ID) (*patientdom.Patient, error)
}

type Repos struct {
	Encounters Encounters
	Patients   Patients // patientrepo satisfies this without knowing emrsvc exists
}
```

Workflows that span domains ("admit a patient" creates an encounter) live in the downstream service (`emrsvc`), which loads both aggregates through its own `Repos` inside one unit of work.

### Trap 4: framework types in the signature

```go
func (s *Service) CreateList(r *http.Request) (*listdom.List, error) // wrong
```

A service takes `context.Context` plus primitives or domain values. That's what lets a queue consumer, a CLI command and a test call the same method. If `net/http` appears in a service package's imports, the transport has leaked in.

### Trap 5: storing the context

```go
type Service struct {
	ctx context.Context // wrong
	uow UnitOfWork
}
```

A context is per call. Storing it in a long-lived struct means every request shares one deadline and one cancellation. Pass it as the first parameter of every method ([context and structs](https://go.dev/blog/context-and-structs)).

### Trap 6: constructing adapters inside the service

```go
func New() *Service {
	db, _ := sql.Open("pgx", os.Getenv("DATABASE_URL")) // wrong
	return &Service{uow: pgtx.New(db).Lists()}
}
```

Now the service imports the adapter, reads config, and can't be tested without a database. `New` takes dependencies; [cmd/api/main.go](../../../../example/cmd/api/main.go) builds them.

## How to spot it in your code

```sh
go list -f '{{join .Imports "\n"}}' ./internal/service/...
```

The output should be the standard library plus your domain packages. `net/http`, `database/sql`, any SDK, any `repo/` or `gateway/` package, or another service means a layer has leaked into the service.

Then read each exported method and check its shape: open UoW → load → call domain → save. Anything else needs a reason.

## The hidden benefit

Services are your **public API in business terms, independent of transport.** Once you have them, every entrypoint is a thin translation:

```go
// HTTP handler
func (h *handler) addItem(w http.ResponseWriter, r *http.Request) {
	var req itemJSON
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if err := h.svc.AddItem(r.Context(), listdom.ID(r.PathValue("id")), listdom.Item(req)); err != nil {
		h.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Queue consumer
func (c *consumer) handle(ctx context.Context, m Message) error {
	return c.svc.AddItem(ctx, listdom.ID(m.ListID), listdom.Item{URL: m.URL})
}

// Test
func TestAddItemMissingList(t *testing.T) {
	err := newService().AddItem(context.Background(), "nope", listdom.Item{URL: "x"})
	if !errors.Is(err, listdom.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}
```

Same use case, three entrypoints, no duplication. And because errors are wrapped with `%w`, every entrypoint can still match `listdom.ErrNotFound` and decide what it means for its transport.

## Self-check

1. What's the difference between an HTTP handler and a service method?
2. Why is `newID` a `func() listdom.ID` and not an interface?
3. A service method is just a repository read. Is that wrong? What's the trade-off?
4. Why does the *delegate* step call a method on `listdom.List` instead of checking rules in the service?
5. Could you call `AddItem` from a Kafka consumer tomorrow? If not, what leaked?

## Things to steal

- **Load, delegate, save.** If a method has a different shape, ask why.
- **One exported method per use case,** named for what the user does.
- **`ctx` first, primitives or domain values after.** Never `*http.Request`, never a stored context.
- **Dependencies through `New`, as interfaces declared in `ports.go` or as function values.**
- **Wrap errors with `%w` and context; log at the edge, once.**
- **Flat services.** No service imports another service; declare what you need in your own `ports.go`.
- **The transport test:** `go list` the imports. No `net/http` allowed.
