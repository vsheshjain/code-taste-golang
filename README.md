# code-taste-golang

A hexagonal-architecture (ports-and-adapters) playbook for Go backends — Domain / Repository / Service / Adapter / Unit-of-Work — written in idiomatic Go rather than ported from another language. Interfaces live with their consumers, constructors return structs, errors are values, `context.Context` comes first, and the layout follows [Organizing a Go module](https://go.dev/doc/modules/layout).

Inspired by [code-taste-python](https://github.com/rakeshbhugra/code-taste-python), which distils *Architecture Patterns with Python* (Cosmic Python). The concepts carry over; the shapes are Go's own.

It also ships as a [Claude Code Skill](https://code.claude.com/docs/en/skills) so Claude can review and structure Go code against these patterns automatically.

## What's in here

```
.
├── .claude-plugin/
│   └── marketplace.json                  # makes this installable as a plugin
├── skills/
│   └── code-taste-golang/
│       ├── SKILL.md                      # the Claude Code skill entrypoint
│       └── docs/
│           ├── plan.md                   # reading plan: go.dev docs → stdlib source
│           └── architecture/             # the playbook
│               ├── README.md             # overview, dependency rule, vocabulary
│               ├── domain.md
│               ├── repository.md
│               ├── service.md
│               ├── adapter.md
│               ├── unit-of-work.md
│               ├── project-structure.md
│               ├── testing.md
│               ├── performance.md
│               ├── logging.md
│               └── instrumentation.md
└── example/                              # runnable, stdlib-only reference module
    ├── go.mod
    ├── cmd/api/main.go                   # composition root
    ├── internal/
    │   ├── list/                         # domain
    │   ├── app/                          # use cases + the interfaces they consume
    │   ├── memory/                       # adapter (in-memory repo + unit of work)
    │   └── httpapi/                      # entrypoint (net/http)
    └── arch_test.go                      # enforces the dependency rule
```

## Run the example

Requires Go 1.24+.

```bash
cd example
go test -race ./...
go run ./cmd/api
# in another shell
curl -s -XPOST localhost:8080/lists -d '{"owner":"u1","name":"My Stuff"}'
```

## Install as a Claude Code skill

### Option 1 — Plugin marketplace (recommended)

Inside Claude Code:

```
/plugin marketplace add vsheshjain/code-taste-golang
/plugin install code-taste-golang@code-taste-golang
/reload-plugins
```

Update with `/plugin marketplace update code-taste-golang` followed by `/reload-plugins`.

### Option 2 — Manual git clone

```bash
git clone https://github.com/vsheshjain/code-taste-golang.git
ln -s "$(pwd)/code-taste-golang/skills/code-taste-golang" ~/.claude/skills/code-taste-golang
```

Claude Code auto-discovers skills in `~/.claude/skills/`. Update with `git pull`; uninstall by removing the symlink.

### Verify

In Claude Code, ask:

> review this Go package against the code-taste architecture

If the skill loaded, Claude will pull the relevant doc(s) from `skills/code-taste-golang/docs/architecture/` and review against the dependency rule and Go-specific traps.

## Use the docs without the skill

Start at `skills/code-taste-golang/docs/architecture/README.md` — it's the entry point and explains the dependency rule and vocabulary used by the rest.

## References

- [Effective Go](https://go.dev/doc/effective_go)
- [Go Code Review Comments](https://go.dev/wiki/CodeReviewComments)
- [Organizing a Go module](https://go.dev/doc/modules/layout)
- [Package names](https://go.dev/blog/package-names)
- [Working with errors in Go 1.13](https://go.dev/blog/go1.13-errors)
- [Contexts and structs](https://go.dev/blog/context-and-structs)
- [Structured logging with slog](https://go.dev/blog/slog)
- *Architecture Patterns with Python* — Harry Percival & Bob Gregory (O'Reilly), the source of the concepts
