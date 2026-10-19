# Project Structure for Hexagonal Go

← [Back to index](./README.md)

What the module layout looks like once you apply the core five concepts — using the conventions the Go team documents in [Organizing a Go module](https://go.dev/doc/modules/layout) rather than a layout ported from another language.

## The canonical layout

```
your-app/
├── AGENTS.md                       # entry map for agents and humans (CLAUDE.md symlinks here)
├── Makefile                        # setup / test / lint / check — `make check` is the definition of done
├── go.mod                          # module github.com/you/your-app
├── go.sum
├── cmd/
│   ├── api/
│   │   └── main.go                 # HTTP server binary — the composition root
│   └── worker/
│       └── main.go                 # optional: background worker binary
│
├── internal/                       # private to this module (enforced by the go tool)
│   ├── list/                       # DOMAIN: List aggregate, rules, sentinel errors
│   │   ├── list.go
│   │   └── list_test.go
│   ├── user/                       # DOMAIN: one package per aggregate once it grows
│   │
│   ├── app/                        # APPLICATION: use cases + the interfaces they consume
│   │   ├── app.go                  # Service, Lists, Repos, UnitOfWork, Summarizer
│   │   └── app_test.go             # service tests over memory.Store + fakes
│   │
│   ├── postgres/                   # ADAPTER: database/sql or pgx implementation
│   │   ├── postgres.go             # DB (UnitOfWork), lists repo over *sql.Tx
│   │   ├── postgres_test.go        # integration tests against a real database
│   │   └── migrations/             # *.sql, embedded with //go:embed
│   ├── memory/                     # ADAPTER: in-memory, for service tests and local runs
│   ├── openai/                     # ADAPTER: Summarizer over the OpenAI API
│   ├── stripe/                     # ADAPTER: payments
│   │
│   ├── httpapi/                    # ENTRYPOINT: net/http handlers, wire DTOs, error→status
│   │   ├── httpapi.go
│   │   ├── middleware.go
│   │   └── httpapi_test.go         # httptest end-to-end
│   └── config/                     # env → typed Config struct (optional; main can do it)
│
├── arch_test.go                    # the dependency-rule check
├── docs/adr/                        # one short file per architectural decision
├── Dockerfile
└── README.md
```

The root files (`AGENTS.md`, `Makefile`, `docs/adr/`) are covered in [Repository Harness](./repository-harness.md).

Compare with the Python layout: there's no `src/`, no `ports/` folder, no `adapters/` umbrella, no `bootstrap.py`, and no `tests/` tree. Each of those has a Go reason, covered below.

## The dependency rule (the only thing you must get right)

```
internal/list, internal/user  ←  stdlib only
internal/app                  ←  domain packages
internal/postgres, memory, …  ←  domain, app (for port types like app.Repos), external SDKs
internal/httpapi              ←  app, domain
cmd/api                       ←  everything (it's the wiring)
```

If `internal/list` imports `internal/postgres`, the architecture is broken. In Go most such mistakes are **compile errors** (import cycle), because the outer package already imports the inner one. The ones that compile — `postgres` importing `httpapi`, `stripe` importing `postgres` — are caught by [`example/arch_test.go`](../../../../example/arch_test.go), which runs `go list -deps` and fails the build on a forbidden edge. If you already run `golangci-lint`, its `depguard` linter does the same job declaratively.

## Why each directory exists

| Directory | One-sentence purpose | If you delete it… |
|---|---|---|
| `go.mod` | Declares the module path and Go version; the unit of versioning | Nothing builds |
| `cmd/<name>/` | One `package main` per binary; builds the world and runs it | Wiring leaks into `init()` and package globals |
| `internal/` | Makes everything under it unimportable from other modules | Every package is public API you must keep stable |
| `internal/<domain>/` | Business structs, rules, sentinel errors | You have CRUD on maps, no business |
| `internal/app/` | Use cases, plus the interfaces they need from outside | Handlers get fat with workflow; nothing to fake |
| `internal/<tech>/` | One adapter per external system, named for the technology | The app can't do anything real |
| `internal/httpapi/` | Transport: decode, call service, encode, map errors to status | No way to invoke the app |

## Go-specific decisions, and why

### Interfaces live with the consumer — so there's no `ports/` package

The Python playbook has a `ports/` directory full of ABCs that adapters inherit from. Go doesn't need it. An interface is satisfied by any type with the right methods, so the package that *uses* the dependency declares the smallest interface it needs, and the adapter never names it:

```go
// internal/app/app.go — the consumer owns the interface
type Lists interface {
	Get(ctx context.Context, id list.ID) (*list.List, error)
	Save(ctx context.Context, l *list.List) error
}
```

This is the Go wiki's guidance verbatim: *"Go interfaces generally belong in the package that uses values of the interface type, not the package that implements those values."* ([Code Review Comments: Interfaces](https://go.dev/wiki/CodeReviewComments#interfaces)). The `app` package therefore plays two roles — services *and* ports — and that's fine; they change together.

Adapters may still import `app` when a port's signature uses an `app` type (the unit of work hands repositories over as `app.Repos`). That's an inward edge and allowed.

### Packages are named for what they provide, not what they contain

From [Package names](https://go.dev/blog/package-names): short, lowercase, single word, no underscores, and no `util`, `common`, `helpers`, `models`, `types`, `interfaces`.

| Avoid | Prefer | Why |
|---|---|---|
| `models/` holding every struct | `list/`, `user/` | The package name is part of every identifier: `list.List`, `list.ErrFull` |
| `adapters/repositories/postgres_list_repository.go` | `postgres/` | The importer writes `postgres.New(db)`; the path already says it's the Postgres one |
| `list.ListService` | `app.Service` | Stutter. The package qualifier already says "list" |
| `interfaces/` | (none — see above) | An interface without a consumer is speculation |
| `pkg/` | `internal/` | `pkg/` is a community convention for *library* code meant to be imported. An application's code should default to private |

### `cmd/<binary>/main.go` is the bootstrap

Python has a `bootstrap.py`; Go has `main`. It reads config, opens connections, builds adapters, injects them into services, and starts the server. Keep `main` tiny by moving the body into `run(ctx) error` so it can return errors and be tested:

```go
func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(log); err != nil {
		log.Error("exit", "err", err)
		os.Exit(1)
	}
}
```

No DI framework. Wiring by hand is a page of plain Go and the compiler checks every edge. See [`example/cmd/api/main.go`](../../../../example/cmd/api/main.go).

### Tests sit next to the code

`list_test.go` lives beside `list.go`. Use `package list_test` (the external test package) by default so tests exercise the public API only, exactly like a caller would. The pyramid is expressed by *which package* a test lives in, not by a `tests/unit/` tree. See [Testing](./testing.md).

### Three kinds of "model", still

```
internal/list/list.go           — the domain struct (List with rules, no tags)
internal/postgres/postgres.go   — the row shape (scan targets, SQL)
internal/httpapi/httpapi.go     — the wire shape (structs with json tags)
```

In Go the temptation is one struct with `json:"..." db:"..."` tags used everywhere. It's fine on day 1. Split the domain struct out the first time the JSON contract and the business rules want different fields.

## What's load-bearing vs. what's flexible

**Load-bearing:**

- The dependency rule, inward only
- The domain package imports nothing but stdlib — no `net/http`, `database/sql`, SDKs, or struct tags
- Interfaces declared by the consumer; constructors return concrete types
- `context.Context` is the first parameter of every method that does I/O
- Services take interfaces as constructor arguments; they never construct adapters
- Handlers are thin: decode, call, encode, map errors

**Flexible:**

- `app` vs `service` vs naming the use-case package after the domain (`listing`). Pick one.
- One `app` package vs one per bounded context (`internal/billing/app`). Start with one.
- Whether the domain packages sit directly under `internal/` or under `internal/domain/`. Flat reads better in imports.
- A single `postgres` package vs `postgres/lists.go` + `postgres/users.go` files. Files first; packages when it gets big.
- `config` package vs parsing env in `main`. Go with `main` until it's more than ~20 lines.

## The test pyramid that comes for free

| Test type | Lives in | What it tests | Speed |
|---|---|---|---|
| **Unit** | `internal/list/*_test.go` | Pure domain rules, table-driven | microseconds |
| **Service** | `internal/app/*_test.go` | Use cases over `memory.Store` and hand-written fakes | milliseconds |
| **Integration** | `internal/postgres/*_test.go` | The real adapter against a real DB (skip with `-short`) | 100s of ms |
| **E2E** | `internal/httpapi/*_test.go` | Full HTTP via `httptest.NewServer` | milliseconds to seconds |

Lots of unit, fewer service, even fewer integration, very few e2e. `go test ./...` runs them all; `go test -short ./...` skips the slow ones.

## A pragmatic warning

This is the *destination*. Don't start here.

1. **Day 1 — one package:** `main.go` with handlers and `database/sql` calls inline. Ship something.
2. **When rules appear in handlers:** pull a domain package out (`internal/list`). Move the `if`s onto methods.
3. **When tests need a database to run:** declare a `Lists` interface in the consumer and add `memory`. Now services test in milliseconds.
4. **When one operation writes two aggregates:** add the unit of work.
5. **When a second external system arrives (LLM, payments):** same shape — interface in `app`, package named after the technology, fake in the test.

For an existing codebase: refactor one handler at a time. Pick one endpoint, get it into this shape, learn from the friction, then do the next.

## Things to steal

- **`cmd/` + `internal/` is the whole skeleton.** Everything else is package naming.
- **The dependency rule is the architecture.** Go's compiler enforces most of it; one small test enforces the rest.
- **Interfaces belong to the consumer.** That single habit removes the `ports/` folder, the factory files, and most mocks.
- **Name packages for what they provide.** `postgres.New`, `list.ErrFull`, `app.Service` — the call site should read like a sentence.
- **`main` is the bootstrap.** Wire by hand; the compiler is your DI container.
- **Earn each layer.** The structure is a destination, not a template.
