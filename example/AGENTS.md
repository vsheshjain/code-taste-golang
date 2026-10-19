# lists-api (example)

A small HTTP service for user-owned reading lists. It is the reference implementation of the code-taste-golang playbook: hexagonal layout, stdlib only, Go 1.24+.

## Commands
- `make setup` — download modules
- `make test`  — fast loop: `go test -short -race ./...`
- `make check` — what CI runs (gofmt, vet, `go mod tidy` diff, race tests). **It must pass before any commit.**
- `make run`   — serve on `:8080` (`ADDR` overrides)

## Layout (dependencies point inward only)
- `internal/list`: domain. Structs, rules and sentinel errors. Stdlib only.
- `internal/app`: use cases, plus the interfaces they consume (`Lists`, `UnitOfWork`).
- `internal/memory`: adapter. In-memory repository and unit of work.
- `internal/httpapi`: entrypoint. Decodes the request, calls the service, encodes the response, and maps errors to status codes.
- `cmd/api`: composition root. The only place that knows every concrete type.

Each package's doc comment states what it is responsible for. Read it with `go doc ./internal/<pkg>`.

## Hard rules
1. Inner packages never import outer ones. `arch_test.go` fails the build if they do.
2. Declare interfaces in the package that consumes them. Constructors return concrete types.
3. `context.Context` is the first parameter of every method that does I/O.
4. Business rules live on domain types, never in `app` or `httpapi`.
5. Domain errors are sentinels matched with `errors.Is`. Only `httpapi.fail` maps them to HTTP statuses.
6. Change one behaviour at a time. Don't refactor unrelated code in the same change.
7. Done means `make check` passes and the new behaviour has a test. "Looks right" is not done.

## Docs (read when relevant)
- `../skills/code-taste-golang/docs/architecture/README.md`: the dependency rule and vocabulary. Read it before adding a package.
- `../skills/code-taste-golang/docs/architecture/testing.md`: where each kind of test goes. Read it before adding tests.
