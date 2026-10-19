---
name: code-taste-golang
description: Review and structure Go backend code against the hexagonal (ports-and-adapters) playbook in ./docs/architecture/, written in idiomatic Go — consumer-side interfaces, cmd/ + internal/ layout, context, wrapped errors, slog, table-driven tests. Use when asked to review Go architecture, structure a new Go service or feature, check whether code follows the patterns (Domain/Repository/Service/Adapter/UoW), or audit what's missing in Go code quality / tooling / tests / observability.
---

# Code Taste (Go)

Code-structure review and guidance, grounded in the architecture playbook at `./docs/architecture/` (sibling to this file). A runnable reference module lives at `../../example/` in the repo root.

The docs define the vocabulary. Don't re-explain concepts they cover — link to the file or quote the relevant part.

## Step 1 — Load only the relevant docs

The full doc set is at `./docs/architecture/`:

| File | Use when… |
|---|---|
| `README.md` | Need the overview / dependency rule / vocabulary cheat-sheet |
| `domain.md` | Reviewing domain structs, methods, sentinel errors, anemic-vs-rich questions |
| `repository.md` | Reviewing data access, `database/sql` usage, in-memory fakes |
| `service.md` | Reviewing use-case orchestration; "rules in service" smells |
| `adapter.md` | Any external system seam (auth, LLM, payment, email, storage); where interfaces live |
| `unit-of-work.md` | Multi-repo writes, transaction boundaries, `BeginTx`/`Commit`/`Rollback` |
| `project-structure.md` | New module setup, `cmd/` + `internal/` layout, package naming, dependency-rule enforcement |
| `testing.md` | Table-driven tests, fakes vs mocks, `httptest`, `-race`, fuzzing |
| `performance.md` | pprof, trace, benchmarks + benchstat, load testing, prod failure modes |
| `logging.md` | `log/slog`, request IDs via context, what to log where |
| `instrumentation.md` | OpenTelemetry spans, metrics, health checks |
| `repository-harness.md` | Making a repo agent-ready: `AGENTS.md`, `make check` as definition of done, executable rules, progress/feature files, clean-state checklist |

**Don't load all of them. Load the 1–3 that match the task.** For a service review: Domain + Service + UoW. For new project setup: Project Structure + Testing. For "what am I missing?": README (vocab/dep rule) + the doc closest to the gap.

## Step 2 — Match task to review mode

**"Review this code":**
- Identify the package's layer (domain / app / adapter / entrypoint / `cmd`)
- Check that layer's "Traps" section from its doc
- Verify the dependency rule (`go list -deps ./internal/<pkg>` — does anything inner import something outer?)
- Check the Go-specific smells below

**"Structure this new feature":**
- Sketch the package layout from `project-structure.md`
- Put each interface in the package that *consumes* it; name the adapter package after the technology (`postgres`, `stripe`)
- Identify the right test boundary (domain unit / service with in-memory adapter / integration / e2e with `httptest`)
- Don't over-layer if the feature is small — match rigor to scope

**"What am I missing?":**
- Compare against the Go tooling baseline: `gofmt`/`goimports`, `go vet`, `staticcheck` or `golangci-lint` (pinned via `go.mod` `tool`), `go test -race` with coverage, `govulncheck`, one `make check` that CI runs verbatim
- Run the fresh-session test from `repository-harness.md`: can a new session learn what/how-organized/how-to-run/how-to-verify/what's-next from files alone?
- List concrete additions in priority order with one-line justifications
- Surface the top 3–5 gaps with the highest impact, not an exhaustive audit

## Step 3 — Apply core principles

- **Concept-first, plain English.** The docs are the vocabulary; reference them, don't re-derive.
- **Calibrated honesty over flattery.** Tell the reader what's wrong, not what's "great with minor improvements."
- **Rent test on every interface.** "Could you name a real second implementation (a test fake counts)?" If not, return the concrete type and drop the interface. Go discovers interfaces; it doesn't design them up front.
- **Match rigor to project stage.** Premature layering, premature packages, premature optimization — all wrong.
- **Tests are the fitness function.** When suggesting refactors, suggest the test that would prove it works.

## Go-specific smells to check

- Interface declared next to its only implementation (producer side) instead of in the consumer package
- Function returns an interface instead of a concrete struct
- Packages named `utils`, `common`, `helpers`, `models`, `types`, `base` — or a `pkg/` dir in an application
- Stutter: `list.ListService`, `user.UserRepository`
- `context.Context` stored in a struct, or not the first parameter of an I/O call
- Errors compared with `==` against wrapped errors, or `err.Error()` string matching; adapter leaking `sql.ErrNoRows`/SDK errors upward
- Log-and-return (same error logged at every layer); `log.Fatal`/`os.Exit` outside `main`
- Domain structs with `json`/`db` tags, or domain importing `database/sql`, `net/http`, an SDK
- Goroutines without a way to stop (no ctx, no `WaitGroup`/`errgroup`); `resp.Body`/`rows` not closed
- Global mutable state or `init()` doing wiring instead of `main`

## Output format

Default to this shape, kept tight:

1. **What's right** — 1–3 bullets, brief. No flattery padding.
2. **What's wrong / missing** — numbered. Each item: the issue, the layer/package it belongs to, the doc reference, the fix shape.
3. **Concrete next steps** — priority-ordered, with rough effort estimate.

Match length to task. A simple "is this right?" gets a 5-line answer. A full code review gets the structured format above.

## What NOT to do

- **Don't re-teach the architecture.** Reference the docs.
- **Don't suggest interfaces without the rent test.** Speculative abstractions are smells — in Go doubly so.
- **Don't import Java/Python shapes.** No DI containers, no abstract base types, no getters/setters, no one-package-per-class.
- **Don't recommend exhaustive refactors on early-stage code.** Lift tooling first; layering can wait.
- **Don't be exhaustive.** Surface the top issues, not every issue.

## Quick reference — the dependency rule (load-bearing)

```
internal/<domain>    ←  imports stdlib only
internal/app         ←  imports domain (declares the port interfaces it consumes)
internal/<adapter>   ←  imports domain, app (for port types), external SDKs
internal/<entrypoint>←  imports app, domain
cmd/<binary>         ←  imports everything (the wiring)
```

Arrows point inward. Go's ban on import cycles catches most violations at compile time; a test running `go list -deps` catches the rest (adapter→adapter, adapter→entrypoint). See `example/arch_test.go`.
