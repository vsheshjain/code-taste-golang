# Testing

← [Back to index](./README.md)

Go ships its test runner, its benchmark harness, its fuzzer, its race detector and its coverage tool in the `go` command. There is no framework to choose. This doc is the orientation: the few concepts that matter, how they map onto the layered layout, and the traps.

## The goal (the only one)

> **Tests give you confidence to change code without breaking things.**

Everything else is a means to that end. Coverage %, TDD purity, "500 tests look impressive" — none of it matters if the tests don't make the codebase safer to change. Conversely, if you can refactor confidently, you have enough tests, whatever the coverage number says.

The architecture exists *because* it makes tests cheap. Pure domain package → fast unit tests. Interfaces declared by the consumer → cheap hand-written fakes. A unit of work that commits only on `nil` → tests that check rollback for free.

## The test pyramid (the central concept)

```
                  ╱──────╲
                 ╱  e2e   ╲          ← few. slow. flaky-prone. test the wires.
                ╱──────────╲
               ╱ integration╲        ← some. medium speed. test adapters
              ╱──────────────╲          against the real thing.
             ╱      unit      ╲      ← many. fast. test domain rules
            ╱──────────────────╲       and service orchestration.
```

**Read it as: the further you go from your domain, the slower and fewer the tests.**

| Type | Package under test | Speed | Count | Example |
|---|---|---|---|---|
| **Unit** | `internal/domain/listdom` (domain) | microseconds | hundreds | `TestAddItemFull` |
| **Service** | `internal/service/listsvc` with `listsvctest.Store` | milliseconds | tens to low hundreds | `TestCreateListAssignsSlug` |
| **Integration** | `internal/repo/listrepo` (or a Postgres `internal/repo/<x>repo`) running the contract suite against the real store | ms to hundreds of ms | tens | `TestFile` |
| **E2E** | `internal/delivery/rest` via `httptest.NewServer` | tens of ms to seconds | a few per critical flow | `TestListLifecycle` |

The inverse pyramid (lots of e2e, few unit) is a smell. It means your business rules can't be exercised without HTTP or a database — which means they aren't actually in the domain package.

## The test types in detail

### Unit tests — the workhorse

Test **one domain package** with **no I/O**: no DB, no HTTP, no filesystem, no wall clock, no randomness you don't control.

The idiomatic shape is the **table-driven test** with `t.Run` subtests ([`list_test.go`](../../../../example/internal/domain/listdom/list_test.go)):

```go
func TestSlugify(t *testing.T) {
	tests := []struct {
		name, want string
	}{
		{"My Stuff", "my-stuff"},
		{"  Go & Rust!  ", "go-rust"},
		{"already-a-slug", "already-a-slug"},
		{"!!!", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := listdom.Slugify(tt.name); got != tt.want {
				t.Errorf("Slugify(%q) = %q, want %q", tt.name, got, tt.want)
			}
		})
	}
}
```

Adding a case is one line. Each case gets its own name in the output, and you can run one with `go test -run 'TestSlugify/My_Stuff'`.

Domain errors are sentinel values, so the assertion is `errors.Is`, never a string compare:

```go
if err := l.AddItem(listdom.Item{URL: "https://go.dev"}); !errors.Is(err, listdom.ErrDuplicateItem) {
	t.Fatalf("duplicate add: err = %v, want ErrDuplicateItem", err)
}
```

If a unit test in `internal/domain/listdom` needs a database driver or an HTTP client, **the code being tested isn't domain** — infrastructure has leaked in. The test is telling you about an architecture problem, not a test problem.

### Service tests — using the `listsvctest` fakes

Test **one use case** with **fakes in place of real adapters**. This is where consumer-side interfaces pay off: `listsvc.Service` takes a `listsvc.UnitOfWork` (declared in [`ports.go`](../../../../example/internal/service/listsvc/ports.go)), and `listsvctest.Store` satisfies it ([`service_test.go`](../../../../example/internal/service/listsvc/service_test.go)):

```go
func TestCreateListAssignsSlug(t *testing.T) {
	ctx := context.Background()
	svc := listsvc.New(&listsvctest.Store{}, func() listdom.ID { return "l1" })

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

Two things make this cheap. The ID generator is an injected `func() listdom.ID`, so IDs are deterministic. And the zero value of `listsvctest.Store` is ready to use, so there is no setup function to forget.

The fakes live in their own package beside the ports they fake — `internal/service/listsvc/listsvctest`, the same pattern as the standard library's `net/http/httptest` and `testing/fstest`. A fake implements *your* interface, not the vendor's API, so it belongs with the interface. Import `<x>svctest` packages only from `_test.go` files; `arch_test.go` fails the build if production code imports one.

Rule violations are service tests too: `TestAddItemRuleViolationSavesNothing` adds a duplicate item and asserts the list still has exactly one.

### Contract tests — keeping the fakes honest

A fake is only useful if it behaves like the real adapter. [`listsvctest/contract.go`](../../../../example/internal/service/listsvc/listsvctest/contract.go) exports one suite that every `listsvc.UnitOfWork` must pass — missing list is `ErrNotFound`, a committed save is readable, an error rolls back:

```go
// listsvctest's own test runs it against the fake…
func TestStore(t *testing.T) {
	listsvctest.TestUnitOfWork(t, func(*testing.T) listsvc.UnitOfWork { return &listsvctest.Store{} })
}

// …and the real adapter's test runs the same suite.
func TestFile(t *testing.T) {
	listsvctest.TestUnitOfWork(t, func(t *testing.T) listsvc.UnitOfWork {
		return listrepo.New(filepath.Join(t.TempDir(), "lists.json"))
	})
}
```

If the fake and the real store ever disagree, one of them fails. It's the same idea as `testing/fstest.TestFS`: the package that defines the contract ships the test for it.

### Integration tests — real adapter, real external system

Test **the adapter itself**, against the real thing. This is the **only** place you need a real database or network. Keep them out of the default `go test ./...` run so the fast loop stays fast. Two standard ways:

```go
//go:build integration

package listrepo_test

func TestPostgres(t *testing.T) {
	db := openTestDB(t) // reads DATABASE_URL, calls t.Skip if unset, registers t.Cleanup
	listsvctest.TestUnitOfWork(t, func(t *testing.T) listsvc.UnitOfWork {
		return listrepo.NewPostgres(db) // hypothetical Postgres adapter; truncates tables per call
	})
}
```

Run with `go test -tags integration ./...`. The alternative is a runtime check:

```go
func TestPostgres(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test; skipped with -short")
	}
	// ...
}
```

Build tags are clearer when the test needs extra dependencies to even compile; `testing.Short()` is fine when it only needs a running service. Pick one per repo and stick to it.

Integration tests live in the adapter's own package (`internal/repo/<x>repo`, `internal/gateway/<vendor>gw`). The example's [`listrepo_test.go`](../../../../example/internal/repo/listrepo/listrepo_test.go) needs no build tag because its "real thing" is a file in `t.TempDir()`. Write **few** of these beyond the contract suite — roughly one per adapter method with non-trivial mapping (row ↔ entity, SQL error ↔ domain error). Their job is to catch SQL and mapping mistakes, not to retest domain rules.

### E2E tests — the full stack

Go's `net/http/httptest` runs your real handler on a real loopback listener with no external process ([`rest_test.go`](../../../../example/internal/delivery/rest/rest_test.go)):

```go
svc := listsvc.New(&listsvctest.Store{}, func() listdom.ID { return "l1" })
srv := httptest.NewServer(rest.NewHandler(svc, slog.New(slog.DiscardHandler)))
defer srv.Close()
```

Then drive it with `http.Client` and assert status codes and bodies. For a single handler without a listener, `httptest.NewRecorder()` plus `handler.ServeHTTP(rec, req)` is even cheaper.

Write **very few** of these — one table per critical flow. Their job is to catch wiring mistakes (wrong route pattern, wrong status mapping), not logic mistakes.

## Test doubles — the four words people confuse

| Term | What it does | In Go |
|---|---|---|
| **Fake** | A working, simpler implementation | `listsvctest.Store`. The default. |
| **Stub** | Returns canned values, no behavior | A struct whose method returns a fixed value |
| **Mock** | Records calls and asserts on expectations | `gomock` / `mockery` generated code |
| **Spy** | A real or fake object that also records calls | A fake with a `calls []Request` field |

**Prefer hand-written fakes.** Go interfaces are small and satisfied implicitly, so a fake is usually ten lines — no generator, no reflection, no `EXPECT().Times(1)` chains. A function field covers one-off behavior:

```go
type fakeSummarizer struct {
	summarize func(ctx context.Context, text string) (string, error)
	calls     []string
}

func (f *fakeSummarizer) Summarize(ctx context.Context, text string) (string, error) {
	f.calls = append(f.calls, text)
	return f.summarize(ctx, text)
}
```

Build the fake once, in the service's `<x>svctest` package next to the port it fakes, and reuse it everywhere. That beats 200 tests each configuring their own generated mock. Mock generators are a sign the interface is too big — see "Rent test" in [adapter.md](./adapter.md).

## The `go test` toolbox — what you actually type

### Arrange / Act / Assert

Every test has three parts; make the shape obvious. You don't need comments, but if you can't see where setup ends and the call begins, the test is too tangled.

### Failure messages: got X, want Y

The standard library style, from [Go Code Review Comments → Useful Test Failures](https://go.dev/wiki/CodeReviewComments#useful-test-failures): say what was called, what came back, and what was expected — in that order.

```go
t.Errorf("Slugify(%q) = %q, want %q", in, got, want)
```

Use `t.Errorf` to keep going and report every failing case; use `t.Fatalf` only when continuing makes no sense (setup failed, a nil you're about to dereference). Assertion libraries like `testify` are optional — they save a few characters and add a dependency. For struct comparison, `reflect.DeepEqual` or `github.com/google/go-cmp/cmp.Diff` (which prints a readable diff) cover most needs.

### Helpers and cleanup

```go
func newService(t *testing.T) *listsvc.Service {
	t.Helper() // failures report the caller's line, not this one
	store := &listsvctest.Store{}
	t.Cleanup(func() { /* close things, runs after the test and its subtests */ })
	return listsvc.New(store, func() listdom.ID { return "l1" })
}
```

`t.Cleanup` replaces most teardown code and runs even when the test fails. `t.Context()` (Go 1.24) returns a context that is cancelled just before cleanup runs — use it instead of `context.Background()` in new tests so goroutines started by the test get told to stop.

### Parallelism

```go
func TestSomething(t *testing.T) {
	t.Parallel()
	// ...
}
```

`t.Parallel()` lets independent tests run concurrently. It only helps if tests don't share state — another reason the zero-value `listsvctest.Store` per test is the right default. Since Go 1.22, loop variables are per-iteration, so the old `tt := tt` copy is no longer needed.

### External test packages

Name test files' package `foo_test` (as the example does: `package listdom_test`, `package listsvc_test`). The test then sees only the exported API, exactly like a real caller. Use the internal `package foo` form only when you genuinely need to test an unexported helper.

### testdata/ and golden files

The `go` tool ignores any directory named `testdata`, so it's the home for fixtures. For large outputs (rendered JSON, generated SQL), compare against a golden file with an `-update` flag:

```go
var update = flag.Bool("update", false, "rewrite golden files")

func TestRenderList(t *testing.T) {
	got := render(sampleList())
	golden := filepath.Join("testdata", "list.golden.json")
	if *update {
		if err := os.WriteFile(golden, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("render mismatch; run go test -update and review the diff")
	}
}
```

### Fuzzing

Native since Go 1.18. Great for parsers and normalizers like `Slugify`:

```go
func FuzzSlugify(f *testing.F) {
	f.Add("My Stuff")
	f.Fuzz(func(t *testing.T, name string) {
		s := listdom.Slugify(name)
		if s != listdom.Slugify(s) {
			t.Errorf("Slugify not idempotent: %q -> %q -> %q", name, s, listdom.Slugify(s))
		}
	})
}
```

`go test` runs only the seed corpus; `go test -fuzz=FuzzSlugify` explores. Failing inputs are saved under `testdata/fuzz/` and become permanent regression cases.

### Time and concurrency: testing/synctest

Code with timeouts, tickers or background goroutines is the classic source of slow, flaky tests. `testing/synctest` (stable in Go 1.25) runs the test in a bubble with a fake clock that advances instantly when every goroutine is blocked:

```go
func TestTimeout(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		defer cancel()
		<-ctx.Done() // returns immediately in real time
		if !errors.Is(ctx.Err(), context.DeadlineExceeded) {
			t.Fatal(ctx.Err())
		}
	})
}
```

No `time.Sleep` in tests, ever.

### Flags you'll use daily

| Command | What it does |
|---|---|
| `go test ./...` | Every package |
| `go test -race ./...` | With the race detector. Run this in CI, always. |
| `go test -count=1 ./...` | Bypass the test cache |
| `go test -run 'TestAddItem$' ./internal/domain/listdom` | One test (regexp) |
| `go test -short ./...` | Skip tests that check `testing.Short()` |
| `go test -tags integration ./...` | Include build-tagged integration tests |
| `go test -coverprofile=c.out ./... && go tool cover -html=c.out` | Coverage, line by line |
| `go test -shuffle=on ./...` | Randomise test order to flush out hidden dependencies |

### The architecture test

The dependency rule is enforced by a test, not by a wiki page. [`arch_test.go`](../../../../example/arch_test.go) runs `go list` over every package, classifies each import by layer, and fails with a WHY/FIX message if, say, `internal/repo/listrepo` imports `internal/delivery/rest`:

```go
var allowed = map[string][]string{
	"domain":   {"domain"},
	"service":  {"domain"},
	"svctest":  {"domain", "service"},
	"repo":     {"domain", "service"},
	"gateway":  {"domain", "service"},
	"delivery": {"domain", "service"},
	"cmd":      {"domain", "service", "repo", "gateway", "delivery"},
}
```

It also rejects service→service imports. `go list`'s `.Imports` excludes test files, which is exactly what lets a repo's `_test.go` import the `<x>svctest` contract suite while production code can't. Go's import-cycle ban already catches some violations; this test catches the rest (adapter importing entrypoint, `cmd` importing a fake, and so on). For bigger repos, `depguard` in `golangci-lint` does the same job declaratively.

## What makes a good test

1. **Fast** — microseconds to milliseconds. Slow tests don't get run.
2. **Isolated** — no shared state. `-shuffle=on` and `t.Parallel()` should never change the result.
3. **Repeatable** — no wall clock, no real randomness, no network. Inject `func() time.Time` and `func() ID`; use `synctest` for timers.
4. **Self-checking** — passes or fails on its own. No "read the log output and decide."
5. **Timely** — written alongside the code, in the same commit.
6. **Readable** — the name states the behavior; the failure message states got and want.

A good test name: `TestAddItemRejectsDuplicateURL`. A bad one: `TestAddItem2`, `TestWorks`.

## The architecture connection (why this matters now)

| Package | Test type | Why it's cheap |
|---|---|---|
| `internal/domain/listdom` | Unit | Pure Go, stdlib only, no setup |
| `internal/service/listsvc` | Service | Inject `&listsvctest.Store{}` and a fixed ID func |
| `internal/service/listsvc/listsvctest` | Contract | The fake runs the same suite as the real adapter |
| `internal/repo/listrepo` (adapter) | Integration | Contract suite against the real store, only in this package |
| `internal/delivery/rest` | E2E | `httptest` + full stack, a handful of cases |
| `cmd/api` | none | `main` only wires; keep logic out so there's nothing to test |

If your handlers contain business rules, every test becomes an HTTP test. The pyramid inverts, the suite slows, people stop running it.

**Architecture and testing are the same conversation.**

## Common traps

### Trap 1: testing implementation, not behavior

```go
// Bad — tests how it's done
mockLists.EXPECT().Save(gomock.Any(), gomock.Any()).Times(1)
svc.CreateList(ctx, "u1", "x")

// Good — tests the outcome
svc.CreateList(ctx, "u1", "x")
got, err := svc.GetList(ctx, "l1")
// assert got.Name == "x"
```

The first breaks when you rename `Save` to `Put` or batch two saves into one. The second only breaks when behavior breaks.

### Trap 2: over-mocking

A test that builds five generated mocks and asserts fourteen expectations is testing the mocks. Use real domain values plus fakes for the ports. Mock only what you literally cannot fake.

### Trap 3: shared state between tests

Package-level variables, a shared `*sql.DB` that tests write to without cleanup, `t.Setenv` in a parallel test (it panics, deliberately). Each test builds its own world. `-shuffle=on` finds the offenders.

### Trap 4: testing every getter and constructor

```go
func TestListName(t *testing.T) {
	l := listdom.List{Name: "x"}
	if l.Name != "x" { t.Fail() }
}
```

Pointless. Test behavior (`New` rejects empty names), not field assignment.

### Trap 5: `time.Sleep` in tests

`time.Sleep(100 * time.Millisecond)` "to let the goroutine finish" is a flake with a timer on it — too short on a loaded CI box, wasted time everywhere else. Synchronise with channels, `sync.WaitGroup`, or `synctest.Wait()`.

### Trap 6: flaky tests left alone

A flaky test is worse than no test: it trains everyone to rerun until green. Run it with `-count=100 -race` to reproduce, then fix or delete it. Never `t.Skip("flaky")` and forget.

### Trap 7: ignoring the race detector

`go test` without `-race` in CI means data races ship. The detector only finds races on executed paths, which is one more reason to have real service tests.

## Pragmatic principles — when to test what

**Always test:**
- Domain rules with real logic (`AddItem` limits, `New` validation)
- Use cases that touch money, auth or data integrity
- Error mapping at seams (SQL error → `listdom.ErrNotFound`, domain error → HTTP status)
- Bug fixes — write the failing test first

**Often skip:**
- `main` and pure wiring
- Throwaway prototypes
- Trivial pass-throughs (the e2e test covers them)

**The cost-vs-bug calculation:** cost of writing the test vs. cost of a bug × probability of a bug. An auth bug is catastrophic; a slug bug is annoying. Test accordingly.

**On TDD:** use it when the behavior is well-specified (parsers, validators) or you're fixing a bug. Skip strict test-first when exploring. What matters is that the tests exist when you ship.

## Self-check

1. Why is the pyramid shaped that way? What goes wrong if it inverts?
2. When would you hand-write a fake versus generate a mock? Why does Go make the first so cheap?
3. Your service test needs a running Postgres. What's wrong, architecturally?
4. A test passes alone but fails with `-shuffle=on`. What's the likely cause?
5. Why is `time.Sleep` in a test a bug, and what replaces it?
6. Why use `package listdom_test` instead of `package listdom` for tests?

## Things to steal

- **Table-driven tests with `t.Run`.** The default shape for anything with more than one case.
- **got X, want Y.** Failure messages that need no debugger.
- **One fake per port, hand-written, reused everywhere.** Small interfaces make this ten lines.
- **Fakes live in `<x>svctest`, beside the ports.** Same pattern as `httptest`; imported only from tests.
- **A contract suite keeps fakes honest.** The fake and the real adapter run the same `TestUnitOfWork`.
- **Zero-value-ready fakes** (`&listsvctest.Store{}`) mean no setup to forget and free parallelism.
- **`-race` in CI, always.** It costs minutes and saves outages.
- **Integration tests behind a build tag or `-short`.** Fast loop locally, full suite in CI.
- **`synctest` instead of `time.Sleep`.** Deterministic time, instant tests.
- **The dependency rule is a test.** `arch_test.go` keeps the architecture true after you stop watching.
- **Coverage is a ratchet, not a target.** Refactor confidence is the real metric.
- **Bug fix? Failing test first. Always.**
