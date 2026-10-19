# Repository Harness (agent-ready repos)

← [Back to index](./README.md)

Adapted from [Learn Harness Engineering](https://walkinglabs.github.io/learn-harness-engineering/en/) (lectures 3–12), translated into Go tooling. That course is about coding agents, but every practice below helps a new human teammate just as much.

## One-line definition

The repository is **the system of record**: everything needed to understand, run, verify and continue the work lives in version-controlled files, and every rule that matters is checked by a command rather than by memory.

The course's core claim: *a harness doesn't make the model smarter; it gives it a closed loop* — explicit rules, boundaries, state, verification, and observability.

## The problem it solves

A capable agent (or a new hire) dropped into an unprepared repo fails in predictable ways:

1. **It can't find the rules.** Decisions live in chat threads, Slack, and someone's head. The agent reinvents them, differently each session.
2. **It loses continuity.** Session two doesn't know what session one finished, half-finished, or decided against.
3. **It declares victory early.** "The code looks right" becomes "done," because nothing in the repo defines *done* as a command that passes.
4. **It overreaches.** Asked for feature A, it also refactors B, and leaves both half-working.
5. **It leaves a mess.** Debug prints, skipped tests, an untidy `go.mod`, a red build — and the next session starts by cleaning up.

## The fresh-session test (the only acceptance test)

Open a brand-new session with nothing but a clone of the repo. It must be able to answer, from files alone:

1. What is this system?
2. How is it organized?
3. How do I run it?
4. How do I verify it?
5. What's the current progress, and what's next?

If any answer requires asking a person, the repo is missing a file.

## What it looks like in a Go repo

```
your-app/
├── AGENTS.md                 # the entry map: overview, commands, hard rules, links (≈50–150 lines)
├── CLAUDE.md -> AGENTS.md    # symlink, so every agent finds the same file
├── Makefile                  # one command surface: setup, test, lint, check
├── go.mod                    # includes `tool` directives so lint versions are pinned
├── docs/
│   ├── adr/                  # one short file per architectural decision
│   │   └── 0001-interfaces-live-with-consumers.md
│   ├── features.md           # feature list with verification commands (when work spans sessions)
│   └── PROGRESS.md           # current state + next steps (only while work is in flight)
├── cmd/api/main.go
├── internal/
│   ├── domain/listdom/doc.go # package doc comment = the package's architecture note
│   └── ...
└── arch_test.go              # the dependency rule as a failing test, with fix hints
```

See [`example/AGENTS.md`](../../../../example/AGENTS.md) and [`example/Makefile`](../../../../example/Makefile) for a working minimum.

## The practices

### 1. A short entry file that is a map, not a manual

`AGENTS.md` is read every session, so it competes with the task for attention. Keep it to four sections:

```markdown
# lists-api

Small HTTP service for user-owned reading lists. Hexagonal layout; stdlib only.

## Commands
- `make setup`  — download modules, install pinned tools
- `make test`   — fast tests (`go test -short -race ./...`)
- `make check`  — everything CI runs; must pass before any commit

## Hard rules (non-negotiable)
1. `internal/domain/*` imports stdlib only. `arch_test.go` enforces this.
2. Interfaces are declared in the consuming service's `ports.go`.
3. `context.Context` is the first parameter of every method that does I/O.
4. Work on one feature at a time; don't refactor unrelated code in the same change.
5. Done = `make check` passes. Not "the code looks right".

## Docs (read only when relevant)
- docs/adr/ — why things are the way they are; read before changing a layer boundary
- docs/features.md — what's done and what's next; read at session start
```

Guidance from the course: no more than ~15 hard rules; put the most critical rules at the top or bottom, not buried in the middle; give every linked doc a one-line *when to read it*. If deleting a rule wouldn't change anyone's decisions, delete it.

### 2. Put facts next to the code — in Go, that's the package doc comment

The course recommends a short `ARCHITECTURE.md` inside each module directory. Go already has that slot: the **package doc comment**. It shows up in `go doc`, on pkg.go.dev, and in every editor hover, and it travels with the code when the package moves.

```go
// Package listsvc holds the list use cases (application services) and, in
// ports.go, the interfaces they need from the outside world.
package listsvc
```

Put the package's responsibility, its boundaries, and its non-obvious constraints there (move it to a `doc.go` once it outgrows the top of a file). Put facts about a type on the type, and facts about a config value on the field. Reserve markdown for decisions that span packages — those go in `docs/adr/`.

### 3. Record decisions, with what you rejected

A decision log stops session three from undoing session one. Each ADR is half a page: date, decision, why, alternatives rejected and why, constraints it creates. The *rejected alternatives* line is the valuable part: it's what stops someone re-proposing a DI framework or a `pkg/` directory every month.

### 4. One command surface, runnable from a clean clone

Agents and humans should never have to guess the incantation. A `Makefile` (or `justfile`, or `magefile`) with a fixed vocabulary:

| Target | Does | Rule |
|---|---|---|
| `setup` | `go mod download`, install tools | Works on a fresh clone, no manual steps |
| `test` | `go test -short -race ./...` | Fast; the inner loop |
| `lint` | `gofmt -l`, `go vet`, `staticcheck`/`golangci-lint` | Zero findings |
| `check` | lint + full tests + `go mod tidy -diff` + `govulncheck` | **The definition of done.** CI runs exactly this |

Pin tool versions in `go.mod` with the Go 1.24+ `tool` directive instead of "install the latest staticcheck":

```bash
go get -tool honnef.co/go/tools/cmd/staticcheck@v0.6.1
go tool staticcheck ./...
```

Now the linter version is in `go.sum`, reviewable, and identical on every machine.

### 5. Make every architectural rule an executable check — with a fix hint

A rule in prose is a suggestion. A rule in a test is architecture. Go helps: import cycles don't compile, `internal/` is enforced by the toolchain, and `go list -deps` makes the rest a ten-line test ([`example/arch_test.go`](../../../../example/arch_test.go)).

The course's key detail is that **failure messages should tell the reader how to fix the problem**, not just that something failed:

```
internal/repo/listrepo (repo) imports internal/httpapi (httpapi)
  WHY: dependencies point inward: domain ← service ← repo/external/httpapi ← cmd
  FIX: move the shared type inward (service ports.go or domain), or do the wiring in cmd/
```

When the same review comment appears twice, promote it to a check: a `depguard` rule, a `go vet` analyzer, or one more case in `arch_test.go`.

### 6. Define "done" as a hierarchy of commands

```markdown
## Definition of Done
1. `go build ./... && go vet ./...`             — static
2. `go test -short -race ./...`                  — unit + service tests
3. `go test -race ./...` (integration enabled)   — real adapters
4. e2e: `httptest` suites / smoke test of `go run ./cmd/api`
Don't start level n+1 while level n fails. Skipping a required level = not done.
```

The verification is run by the harness (CI, `make check`), not judged by whoever wrote the code. The course goes further and recommends a separate reviewer — another agent or a person — so the author never grades their own work.

### 7. A feature list with verification commands (when work spans sessions)

For multi-session work, keep `docs/features.md` (or JSON). Every entry needs an id, the behavior, the command that proves it, a state, and evidence:

```markdown
| id  | behavior                                   | verification                                         | state       | evidence |
|-----|--------------------------------------------|------------------------------------------------------|-------------|----------|
| F01 | POST /lists creates a list with a slug     | go test ./internal/httpapi -run TestListLifecycle    | passing     | a1b2c3d  |
| F02 | Adding the 101st item returns 409          | go test ./internal/domain/listdom -run TestAddItemFull         | passing     | a1b2c3d  |
| F03 | GET /lists?owner=u1 lists a user's lists   | go test ./internal/httpapi -run TestListByOwner      | active      |          |
```

States are `not_started`, `active`, `blocked`, `passing`. Only **one** `active` at a time. Size each feature to one session ("add an item to a list", not "build lists"). In Go, the verification is almost always a `go test -run` command — which is a strong nudge to write the test first.

### 8. Progress and handoff, in files

While work is in flight, `docs/PROGRESS.md` holds: last commit and test status, completed, in progress (with the blocker), known issues, and a numbered *next steps* list. Session start: read it, run `make check`, continue from next steps. Session end: update it, run `make check`, commit.

Commit after each atomic, verified unit of work, with messages that say *why*. Git history becomes free checkpoints; an abandoned attempt is a `git restore`, not a half-merge.

Delete `PROGRESS.md` when the work lands. A stale progress file is worse than none: it confidently points the next session the wrong way.

### 9. Leave a clean state, every session

The exit checklist for a Go repo:

- [ ] `make check` passes (fmt, vet, lint, race tests)
- [ ] `go mod tidy -diff` is empty
- [ ] no leftover `fmt.Println`/`log.Printf` debugging, no stray `t.Skip`, no commented-out tests
- [ ] feature list / progress updated
- [ ] `go run ./cmd/api` still starts (the standard startup path works)
- [ ] `git status` clean — everything committed or deliberately discarded

### 10. Observability the next session can read

The agent debugging a failure needs the same evidence a human would: startup/ready/shutdown events, the failing request, the full error chain. Structured JSON logs via `log/slog` and wrapped errors (`%w`) make that greppable without guesswork. See [Logging](./logging.md) and [Instrumentation](./instrumentation.md).

## What's in / what's out

**In the repo:** run commands, hard rules, decisions and their rejected alternatives, the definition of done, in-flight progress, the checks that enforce architecture.

**Out of the repo:** secrets; transient debug output (`/tmp`, not the tree); essays that restate what `go doc` already shows; anything nobody would miss if deleted.

## The traps

### Trap 1: one giant instruction file

A 1,000-line `AGENTS.md` dilutes the rules that matter. The course cites research where large repo context files raised inference cost by over 20% without improving success. Keep the entry file a map; move topic detail into docs that are loaded only when relevant.

### Trap 2: docs that duplicate the code

Restating a function's signature in markdown guarantees the two will drift. If godoc can carry it, godoc carries it.

### Trap 3: rules nobody enforces

"Domain must not import `database/sql`" in a README is a wish. In `arch_test.go` it's a fact. If a rule matters, it fails a build.

### Trap 4: stale state files

An out-of-date `PROGRESS.md` or ADR that no longer matches the code is worse than nothing. Update docs in the same commit as the code, and delete state files when the work is done.

### Trap 5: "done" without a command

"Implemented F03" with no passing `go test -run` is a claim, not a result. Evidence is a commit hash and a green command.

### Trap 6: harness for its own sake

Feature lists, progress files and rubrics have the same rent test as interfaces. A two-hour change needs `make check` and nothing else. The course suggests periodically disabling one harness component; if nothing gets worse, leave it off.

## How to spot it in your code

- Run the fresh-session test: a new session, only the repo, the five questions.
- Is there one command that means "done"? Does CI run exactly that command?
- `grep -rn "TODO\|fmt.Println\|t.Skip" --include=*.go .` — debris from earlier sessions?
- Pick a rule from the README. Delete the code it protects against, in a scratch branch. Does any check fail?
- `git log` — are commits atomic, with a *why*, or "wip" / "fix" / "more"?

## Self-check

1. Where does a Go package's architecture note belong, and why is that better than a markdown file beside it?
2. What's the difference between `make test` and `make check`, and which one does CI run?
3. Why must a failing architecture check print a FIX line?
4. When should `docs/PROGRESS.md` exist, and when should it be deleted?
5. Why is "only one feature `active`" a rule, not a suggestion?

## Things to steal

- **The repo is the system of record.** If it isn't in git, it doesn't exist for the next session.
- **Fresh-session test.** Five questions, answered from files alone.
- **`AGENTS.md` is a map**, ~100 lines, ≤15 hard rules, links with *when to read*.
- **Godoc is Go's per-module architecture note.** Use it before reaching for markdown.
- **`make check` is the definition of done**, and CI runs exactly it.
- **Every rule that matters fails a build, and the failure says how to fix it.**
- **One feature active at a time; done means a command passed.**
- **Leave it clean:** green check, tidy `go.mod`, no debris, everything committed.
