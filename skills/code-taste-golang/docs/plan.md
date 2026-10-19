# Learning Good Go — Reading Plan

Goal: build a real taste model for what good Go looks like. Go has an unusually clear house style, written down by the people who built the language, and a standard library that is itself the best example codebase. Read the guidance first, then the code that embodies it, then one real application. Don't skim — read actively.

## Stage 1 — The official guidance (go.dev/doc), in order

Short documents, high density. Most are an evening each.

1. **[A Tour of Go](https://go.dev/tour/)** — skip if you already write Go; otherwise do it all, including the concurrency section.
2. **[How to Write Go Code](https://go.dev/doc/code)** — modules, packages, the `go` command. The vocabulary for everything after.
3. **[Effective Go](https://go.dev/doc/effective_go)** — the house style. Read the sections on names, interfaces, embedding, errors and concurrency twice. Note that it predates modules and generics; the idioms still hold.
4. **[Organizing a Go module](https://go.dev/doc/modules/layout)** — `cmd/`, `internal/`, and why there's no `src/` or `pkg/` requirement. The basis for [project-structure.md](./architecture/project-structure.md).
5. **[Go Code Review Comments](https://go.dev/wiki/CodeReviewComments)** — the checklist reviewers actually use: receiver names, error strings, interfaces on the consumer side, "useful test failures".
6. **[Go Proverbs](https://go-proverbs.github.io/)** — "The bigger the interface, the weaker the abstraction." "A little copying is better than a little dependency." "Errors are values." Keep them nearby.
7. **Blog posts that sharpen the rest:**
   - [Package names](https://go.dev/blog/package-names) — why `list.List` and not `models.ListModel`; no `util`, `common`, `helpers`.
   - [Working with Errors in Go 1.13](https://go.dev/blog/go1.13-errors) and [Errors are values](https://go.dev/blog/errors-are-values) — `%w`, `errors.Is`, `errors.As`, and designing around errors.
   - [Go Concurrency Patterns: Context](https://go.dev/blog/context) and [Contexts and structs](https://go.dev/blog/context-and-structs) — why `ctx` is the first parameter and never a struct field.
   - [Structured Logging with slog](https://go.dev/blog/slog).
   - [Routing Enhancements for Go 1.22](https://go.dev/blog/routing-enhancements) — method and wildcard patterns in `ServeMux`.

**Time budget:** 1–2 weeks of evenings.

---

## Stage 2 — The standard library, read as a codebase

The stdlib is the canonical "good Go". Read with the godoc open alongside the source (`go doc -src`, or [cs.opensource.google/go](https://cs.opensource.google/go/go)).

**Reading order (don't deviate):**

1. **`io`** — `io.go` only. `Reader`, `Writer`, `Closer` and their combinations. The smallest interfaces with the largest reach; the model for every port you'll design.
2. **`net/http`** — `server.go`: `Handler`, `HandlerFunc`, `Server.Serve`, how one goroutine per connection is born. Then `servemux.go` / the `ServeMux` section for pattern routing. Skip `h2_bundle.go` and the transport on a first pass.
3. **`database/sql`** — `sql.go`: how `DB` is a pool, how `Tx` is a unit of work, how `driver` interfaces keep the package vendor-neutral. This *is* the ports-and-adapters pattern, in the standard library.
4. **`log/slog`** — `logger.go`, `handler.go`, `record.go`. A modern API designed with generics-era Go: a small `Handler` interface, a concrete `Logger`, and careful allocation avoidance.
5. *(skip on first pass)* `context`, `sync`, `encoding/json` — read once the above feel familiar.

**Time budget:** 2–3 weeks of evenings.

**Lens:** for every interface you meet, ask *who declares it* (the consumer or the implementer?), *how many methods* it has, and *how many implementations exist*. That's the rent test from [adapter.md](./architecture/adapter.md), applied by the people who wrote the language.

---

## Stage 3 — One real application

The stdlib is libraries. To see application layering, read one service end to end.

**[benbjohnson/wtf](https://github.com/benbjohnson/wtf)** — a small but complete web app built around the "Standard Package Layout": domain types and interfaces in the root package, one package per dependency (`sqlite`, `http`, `mock`), wiring in `cmd/wtfd`. It's the closest real-world cousin of this playbook, with one deliberate difference: it declares interfaces in the domain package rather than with the consumer. Note where you'd make the other choice and why.

**Reading order:**
1. The root package (`wtf.go`, `dial.go`, `user.go`) — domain types, errors, service interfaces
2. `sqlite/` — the adapter: transactions, mapping rows to domain types
3. `http/` — the entrypoint: handlers, error-to-status translation
4. `cmd/wtfd/main.go` — the composition root
5. *(skip)* front-end templates and assets

**Alternative:** the Go source's own commands under [`src/cmd/`](https://github.com/golang/go/tree/master/src/cmd), e.g. `cmd/gofmt` (small, readable) — good for seeing how the Go team structures `main` and `internal/` in a real tool.

---

## Active reading technique (do this for every stage)

Keep a notes file with **two columns**:

| Things I'd steal | Things I'd do differently |
| --- | --- |
| ... | ... |

Forces active reading. After all three stages you'll have a real, defensible taste — and you'll be able to say *why* an idiom exists, not just that it does.

## Rules

- No top-to-bottom reading. Follow the order above.
- One file at a time. Finish it before moving on.
- If you don't understand something, write the question down — don't skip silently.
- Read the code before the tests. Predict the behavior, then check the `_test.go` files to verify.
- When a stdlib choice surprises you, find the proposal or issue that introduced it (`go.dev/issue/NNNN`). The reasoning is usually written down.
