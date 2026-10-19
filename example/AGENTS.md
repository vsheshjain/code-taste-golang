# lists-api (example)

A small HTTP service for user-owned reading lists. It is the reference implementation of the code-taste-golang playbook: hexagonal layout, stdlib only, Go 1.24+.

## Commands
- `make setup` — download modules
- `make test`  — fast loop: `go test -short -race ./...`
- `make check` — what CI runs (gofmt, vet, `go mod tidy` diff, race tests). **It must pass before any commit.**
- `make run`   — serve on `:8080` (`ADDR` overrides), storing data in `lists.json` (`DATA_FILE` overrides)

## Layout (dependencies point inward only)
- `internal/domain/listdom`: domain. Structs, rules and sentinel errors. Stdlib only.
- `internal/service/listsvc`: use cases. `ports.go` declares everything the service needs from outside (`Store`, `Repos`, `UnitOfWork`).
- `internal/service/listsvc/listsvctest`: fakes for those ports, plus the `TestUnitOfWork` contract suite. Import it from `_test.go` files only.
- `internal/repo/listrepo`: storage adapter (a JSON file, swapped into place atomically). Its tests run the same contract suite as the fake.
- `internal/httpapi`: entrypoint. Wire DTOs are in `dto.go`. It decodes the request, calls the service, encodes the response, and maps errors to status codes.
- `cmd/api`: composition root. The only place that knows every concrete type.

Each package's doc comment states what it is responsible for. Read it with `go doc ./internal/<pkg>`.

## Hard rules
1. Dependencies point inward: domain ← service ← repo/external/httpapi ← cmd. Fakes are imported only from tests. `arch_test.go` fails the build otherwise.
2. Declare interfaces in the consuming service's `ports.go`. Constructors return concrete types. Services never import other services.
3. `context.Context` is the first parameter of every method that does I/O.
4. Business rules live on domain types, never in `listsvc` or `httpapi`.
5. Domain errors are sentinels matched with `errors.Is`. Only `httpapi.fail` maps them to HTTP statuses.
6. Change one behaviour at a time. Don't refactor unrelated code in the same change.
7. Done means `make check` passes and the new behaviour has a test. "Looks right" is not done.

## Docs (read when relevant)
- `../skills/code-taste-golang/docs/architecture/README.md`: the dependency rule and vocabulary. Read it before adding a package.
- `../skills/code-taste-golang/docs/architecture/testing.md`: where each kind of test goes. Read it before adding tests.
- `../skills/code-taste-golang/docs/architecture/project-structure.md`: where each kind of code, port, fake and DTO goes, and why. Read it before adding a domain or adapter.
