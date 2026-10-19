# Project Structure for Hexagonal Go

← [Back to index](./README.md)

What the module layout looks like once you apply the core five concepts. It follows the Go team's conventions in [Organizing a Go module](https://go.dev/doc/modules/layout) and [Package names](https://go.dev/blog/package-names). It also keeps the layers visible as folders, so anyone used to layered architectures in other languages can find their way around immediately.

## The canonical layout

A multi-domain service (patients, EMR records, hospitals):

```
your-app/
├── AGENTS.md                          # entry map for agents and humans (CLAUDE.md symlinks here)
├── Makefile                           # setup / test / lint / check — `make check` is the definition of done
├── go.mod                             # module github.com/you/your-app
├── cmd/
│   └── api/main.go                    # composition root: builds repos + external clients, injects into services
│
├── internal/                          # private to this module (enforced by the go tool)
│   ├── domain/                        # PURE: entities, rules, sentinel errors. Stdlib only, no I/O
│   │   ├── patientdom/                # package patientdom → patientdom.Patient, patientdom.ErrNotFound
│   │   │   ├── patient.go
│   │   │   └── patient_test.go        # table-driven rule tests
│   │   ├── emrdom/                    # may import patientdom (one direction only)
│   │   └── hospitaldom/
│   │
│   ├── service/                       # USE CASES + PORTS
│   │   ├── patientsvc/
│   │   │   ├── ports.go               # Store, Repos, UnitOfWork, plus port DTOs: everything this service needs from outside
│   │   │   ├── service.go             # Service + use-case methods
│   │   │   ├── service_test.go        # tests over patientsvctest fakes
│   │   │   └── patientsvctest/        # FAKES for patientsvc's ports (like net/http/httptest)
│   │   │       ├── store.go           # in-memory Store + UnitOfWork with real rollback
│   │   │       └── contract.go        # TestUnitOfWork(t, newUoW): one suite run against fake AND real repo
│   │   ├── emrsvc/
│   │   │   ├── ports.go               # Encounters, Patients (its own small view), EHRClient
│   │   │   ├── service.go
│   │   │   └── emrsvctest/            # in-memory stores + FakeEHR (records calls, canned replies)
│   │   └── hospitalsvc/
│   │
│   ├── repo/                          # STORAGE ADAPTERS: data the app owns
│   │   ├── patientrepo/               # Postgres impl of patientsvc.Store; its tests run patientsvctest's contract
│   │   ├── emrrepo/
│   │   ├── hospitalrepo/
│   │   └── pgtx/                      # opens one *sql.Tx and gives each service its UnitOfWork
│   │       └── migrations/            # *.sql, embedded with //go:embed
│   │
│   ├── external/                      # EXTERNAL ADAPTERS: third-party systems, one package per system
│   │   ├── epic/                      # implements emrsvc.EHRClient
│   │   └── openai/
│   │
│   └── httpapi/                       # ENTRYPOINT: handlers, wire DTOs (dto.go), error → status
│
├── arch_test.go                       # the dependency rule, with WHY/FIX failure messages
├── docs/adr/                          # one short file per architectural decision
└── README.md
```

The root files (`AGENTS.md`, `Makefile`, `docs/adr/`) are covered in [Repository Harness](./repository-harness.md). A working single-domain version is in [`example/`](../../../../example/): `domain/listdom`, `service/listsvc` (+ `listsvctest`), `repo/listrepo`, `httpapi`, `cmd/api`.

## The dependency rule (the only thing you must get right)

```
domain/<x>dom          ←  stdlib + upstream domains only (emrdom → patientdom, never back)
service/<x>svc         ←  domain/*            (ports.go declares everything else it needs)
service/<x>svc/<x>svctest ← its service + domain (imported only from _test.go files)
repo/*, external/*     ←  domain/*, service/* (they implement ports)
httpapi                ←  domain/*, service/*
cmd/api                ←  everything except *svctest
```

Two extra rules: **a service never imports another service** (it declares a small interface in its own `ports.go` instead), and **fakes are never imported by production code**.

Go's compiler enforces part of this for free, because import cycles don't compile. [`example/arch_test.go`](../../../../example/arch_test.go) enforces the rest: it reads every package's direct imports with `go list`, classifies each by layer, and fails with a WHY and a FIX line on any forbidden edge.

## Why each piece exists

| Piece | Purpose | If you delete it… |
|---|---|---|
| `cmd/<name>/` | One `package main` per binary; builds the world and runs it | Wiring leaks into `init()` and globals |
| `internal/` | Nothing outside the module can import these packages | Every package becomes public API |
| `domain/<x>dom` | Business types, rules, sentinel errors | You have CRUD on maps, no business |
| `service/<x>svc` | Use cases, plus the ports they need | Handlers get fat with workflow; nothing to fake |
| `ports.go` | One file per service listing its outside needs | The seams are scattered and hard to see |
| `<x>svctest` | Fakes + contract suites for that service's ports | Every test hand-rolls its own fake, and the fakes drift |
| `repo/<x>repo` | Persistence for one domain | Storage leaks into services |
| `repo/pgtx` | One transaction spanning several domains' repos | Cross-domain writes can't be atomic |
| `external/<tech>` | One adapter per third-party system | SDK types leak into services |
| `httpapi` | Transport: decode, call, encode, map errors | No way to invoke the app |

## The design choices, and why

### 1. `domain/` is a folder of packages, each suffixed `dom`

Domains are the heart of the app, so they get a visible home. Each subfolder is its own package, and the folder name matches the package name, as Go convention requires. The suffix is what makes every layer importable side by side without aliases:

```go
import (
	".../internal/domain/patientdom"
	".../internal/service/patientsvc"
	".../internal/repo/patientrepo"
)

p, err := patientdom.New(id, name)      // the entity
svc := patientsvc.New(uow)              // the use cases
repo := patientrepo.New(tx)             // the storage
```

At every call site the reader knows which layer a name comes from. Without suffixes, three packages called `patient` would collide in `main.go`.

### 2. Ports live in the service that consumes them, in `ports.go`

Go interfaces are satisfied implicitly: `patientrepo` never has to mention `patientsvc.Store`, it just has the right methods. The Go convention is therefore that *the consumer declares the interface* ([Code Review Comments: Interfaces](https://go.dev/wiki/CodeReviewComments#interfaces)). Putting every port in a file named `ports.go` keeps the seams as easy to find as a dedicated folder would. It also avoids the import cycles a shared `ports` package tends to cause.

A service that needs another domain's data doesn't import that domain's service. `emrsvc/ports.go` declares `type Patients interface { Get(ctx, patientdom.ID) (*patientdom.Patient, error) }`, and `patientrepo` satisfies it as-is.

### 3. Storage and third-party systems are separate adapter folders

`repo/` is data the app **owns**: schemas it migrates, transactions it controls. `external/` is systems it **calls**: Epic, OpenAI, Stripe. They fail differently (constraint violations vs timeouts and rate limits), they're tested differently (contract suite against a local DB vs canned replies), and they change for different reasons. Separate folders make that difference visible.

Repos are per domain (`patientrepo`), so each domain's persistence is easy to find. A domain-spanning transaction is handled by `pgtx`. Each repo takes a small `Querier` interface that both `*sql.DB` and `*sql.Tx` satisfy, so `pgtx` can open one transaction and build every repo on it.

### 4. Fakes live beside the ports, in `<x>svctest`

A fake implements *the port*, not the vendor. `FakeEHR` imitates `emrsvc.EHRClient`, not Epic's API, so it belongs with `emrsvc`. The standard library uses the same pattern: `net/http/httptest`, `testing/fstest`, `io/iotest`.

Each `<x>svctest` also ships a **contract suite** (`TestUnitOfWork(t, newUoW)`). The fake runs it, and the real repo runs the same suite. If production behaviour and the fake ever disagree, a test fails, so service tests can't pass against a fake that lies.

### 5. Three homes for DTOs

| DTO kind | Lives in | Example |
|---|---|---|
| Wire (JSON request/response) | `httpapi/dto.go` | `createListRequest`, `listResponse` |
| Port (inputs/outputs of an outside system) | the service's `ports.go`, next to the interface | `SummaryRequest`, `ChargeResult` |
| Persistence row shapes | unexported, inside the repo package | `listRow` in `listrepo` |

Domain types carry **no struct tags**. Each of the three shapes changes for different reasons — the API contract, the vendor, the schema — and keeping them apart means one can change without touching the domain.

### 6. `cmd/<binary>/main.go` is the bootstrap

It reads config, opens connections, builds adapters, injects them into services, and starts the server. Keep `main` tiny by moving the body into `run() error`. No DI framework: wiring by hand is a page of plain Go, and the compiler checks every edge. See [`example/cmd/api/main.go`](../../../../example/cmd/api/main.go).

### 7. Tests sit beside the code

`go test` requires `_test.go` files to live next to the package they test. Use the external test package (`package listsvc_test`) by default, so tests exercise only the public API, like any other caller. The test pyramid comes from *which layer* a test sits in, not from a `tests/unit/` tree. See [Testing](./testing.md).

## Coming from a layered Python (or Java) codebase

| Familiar layout | This layout | Same idea? |
|---|---|---|
| `domain/lists.py` | `internal/domain/listdom/` | Yes |
| `services/lists.py` | `internal/service/listsvc/service.go` | Yes |
| `ports/` folder of ABCs | `ports.go` inside each service | Same seams, consumer-declared (choice 2) |
| `adapters/repositories/` | `internal/repo/` | Yes |
| `adapters/auth/`, `adapters/llm/` | `internal/external/` | Yes, named for what they are (choice 3) |
| `adapters/*/fake.py` | `internal/service/<x>svc/<x>svctest/` | Fakes beside the ports (choice 4) |
| `entrypoints/api/` + `schemas/` | `internal/httpapi/` + `dto.go` | Yes (choice 5) |
| `bootstrap.py` | `cmd/api/main.go` | Yes (choice 6) |
| `tests/unit/`, `tests/service/`… | `_test.go` beside each file | Go toolchain requirement (choice 7) |
| `src/your_app/` | `internal/` | Same role, plus import privacy |

## What's load-bearing vs. what's flexible

**Load-bearing:**

- The dependency rule, and the arch test that enforces it
- Domain packages import stdlib only (plus upstream domains); no struct tags, no `database/sql`, no SDKs
- Ports declared in the consuming service's `ports.go`; constructors return concrete types
- Services never import services or adapters
- Fakes only from `_test.go`
- `context.Context` first on every I/O method

**Flexible:**

- The suffixes (`dom`/`svc`/`repo`). Pick one scheme and use it everywhere.
- `repo/<x>repo` per domain vs a single `repo/postgres` package. Per domain until the shared transaction code gets painful.
- Whether `pgtx` exists. Not until a use case writes two domains atomically.
- Splitting `ports.go` into `ports_store.go`/`ports_ehr.go` when it grows.
- A `config` package vs parsing env in `main`. Use `main` until it's more than ~20 lines.

## The test pyramid that comes for free

| Test type | Lives in | Tests | Speed |
|---|---|---|---|
| **Unit** | `domain/<x>dom/*_test.go` | Domain rules, table-driven | microseconds |
| **Service** | `service/<x>svc/*_test.go` | Use cases over `<x>svctest` fakes | milliseconds |
| **Contract / integration** | `repo/<x>repo/*_test.go`, `external/<tech>/*_test.go` | The real adapter, run through the contract suite (DB ones skipped with `-short`) | 100s of ms |
| **E2E** | `httpapi/*_test.go` | Full HTTP via `httptest.NewServer` | ms–s |

## A pragmatic warning

This is the *destination*. Don't start here.

1. **Day 1:** `main.go` with handlers and `database/sql` calls inline. Ship something.
2. **When rules appear in handlers:** pull out `domain/<x>dom` and move the `if`s onto methods.
3. **When tests need a database:** add `service/<x>svc` with `ports.go`, plus `<x>svctest` fakes. Services now test in milliseconds.
4. **When storage gets real:** add `repo/<x>repo` and run the contract suite against it.
5. **When a second domain arrives:** another `<x>dom`/`<x>svc`/`<x>repo` triple. Add `pgtx` only when a use case writes both domains atomically.
6. **When a third-party system arrives:** port in `ports.go`, adapter in `external/<tech>`, fake in `<x>svctest`.

## Things to steal

- **Layers are folders; domains are subfolders; packages carry a layer suffix.** `patientdom`, `patientsvc` and `patientrepo` read unambiguously at every call site.
- **`ports.go` makes the seams visible** without fighting Go's consumer-side interfaces.
- **`repo/` for data you own, `external/` for systems you call.**
- **Fakes beside the ports, with a contract suite** that the real adapter must also pass.
- **Three DTO homes:** wire in `httpapi`, port DTOs in `ports.go`, rows inside the repo.
- **The dependency rule is the architecture,** and `arch_test.go` keeps it true.
