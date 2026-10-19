# Performance & Benchmarking

← [Back to index](./README.md)

Performance work is its own discipline. This doc is the orientation, not a deep dive. Go is unusual in how much of the toolkit ships with the toolchain — profiler, tracer, benchmark harness, escape analysis — so the hard part is picking the right tool, not installing one.

> **Companion doc:** this page covers the *external* tooling — profilers, tracers, load testers, monitors. For what to wire *into* your code (spans, health checks, metrics, benchmarks), see [instrumentation.md](./instrumentation.md).

## The goal (the only one)

> **Know whether your system is fast enough to keep users happy, and have the tools to find out why when it isn't.**

Microbenchmarks, "we cut allocations by 40%" blog posts, premature `sync.Pool` — none of it matters if user-observable latency is fine. If users are unhappy, no benchmark win matters until you fix the actual problem.

## The mental model — 5 questions, 5 tools

| Question | Tool | When |
|---|---|---|
| **"Is function X slow / allocating?"** | Profiler — `pprof` (CPU, heap, allocs, block, mutex, goroutine) | You suspect specific code. `go test -cpuprofile`, or `net/http/pprof` on a running server. |
| **"Where is request latency going?"** | Distributed tracing — OpenTelemetry → Jaeger/Tempo/Honeycomb/Datadog | An endpoint takes 800ms and you don't know where. |
| **"Why is it slow but the CPU is idle?"** | Execution tracer — `runtime/trace` + `go tool trace` | Scheduler latency, GC pauses, goroutines blocked on channels or syscalls. |
| **"Will it survive N concurrent users?"** | Load testing — k6, vegeta, hey | Pre-launch capacity or proving an SLO. |
| **"What's slowing my DB?"** | `EXPLAIN ANALYZE`, `pg_stat_statements`, `sql.DBStats` | Usually the answer. Most backend slowness is at the DB. |

Reach for the wrong one and you waste a day. *"My endpoint is slow"* → start with **tracing** to find which span, not with a CPU profile of the whole process.

## The pragmatic principles (the load-bearing ones)

1. **Don't optimize without measuring.** "I bet this allocates" is wrong most of the time. Profile first.
2. **Most slowness is I/O, not CPU.** DB queries > network calls > JSON encoding > your Go code. Go is fast enough that the CPU is rarely the bottleneck in a CRUD service.
3. **p99 matters more than p50.** GC pauses, lock contention and pool exhaustion all show up in the tail first.
4. **Profile with prod-shaped traffic.** A heap profile under synthetic load lies about what real users allocate.
5. **The cheapest win is still N+1 queries.** A loop calling `repo.Get` per item is the Go version of the ORM lazy-load trap.
6. **Set latency budgets early.** *"POST /lists < 200ms p99."* Alert on it. Without a budget, regressions are invisible.

## Profiling with pprof

Two ways in, same viewer.

**From a test or benchmark:**

```sh
go test -run=^$ -bench=BenchmarkSlugify -cpuprofile=cpu.out -memprofile=mem.out ./internal/domain/listdom
go tool pprof -http=:0 cpu.out
```

**From a running server** — mount `net/http/pprof` on a separate, non-public listener:

```go
import (
	"net/http"
	"net/http/pprof"
)

func debugServer(addr string) *http.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
	return &http.Server{Addr: addr, Handler: mux} // e.g. "localhost:6060", never the public port
}
```

```sh
go tool pprof -http=:0 http://localhost:6060/debug/pprof/profile?seconds=30
go tool pprof -http=:0 http://localhost:6060/debug/pprof/heap
curl -s localhost:6060/debug/pprof/goroutine?debug=2 | less   # every goroutine's stack
```

Don't use the blank import `_ "net/http/pprof"` in a service — it registers on `http.DefaultServeMux`, and if anything serves that mux publicly you've exposed your internals.

The flame graph view (`-http`) answers "where is CPU going?" in seconds. The `alloc_space` sample of the heap profile answers "who allocates the most?", which is usually what GC pressure traces back to.

## The execution tracer

`pprof` samples *where CPU time goes*. It can't see time spent *not* running — waiting for a lock, a channel, the scheduler, or the GC. `runtime/trace` records those events:

```sh
curl -o trace.out 'localhost:6060/debug/pprof/trace?seconds=5'
go tool trace trace.out
```

Reach for it when latency is high but CPU is low: goroutines stuck on a mutex, a worker pool too small, GC assists stealing time from requests.

## Benchmarks: b.Loop and benchstat

Write benchmarks next to the code ([`list_test.go`](../../../../example/internal/domain/listdom/list_test.go)):

```go
func BenchmarkSlugify(b *testing.B) {
	for b.Loop() {
		listdom.Slugify("A Fairly Long List Name With Some Punctuation!")
	}
}
```

`b.Loop()` (Go 1.24) replaces `for i := 0; i < b.N; i++`: setup outside the loop is excluded from timing automatically, and the compiler can't optimise the call away.

A single benchmark run is noise. Compare before/after with `benchstat`:

```sh
go test -run=^$ -bench=Slugify -count=10 ./internal/domain/listdom > old.txt
# make the change
go test -run=^$ -bench=Slugify -count=10 ./internal/domain/listdom > new.txt
go run golang.org/x/perf/cmd/benchstat@latest old.txt new.txt
```

`benchstat` reports the delta and whether it's statistically significant. "~" means no detectable change — stop optimising. Add `-benchmem` to see allocations per op.

## Escape analysis and PGO

**Escape analysis** tells you what the compiler moved to the heap:

```sh
go build -gcflags='-m' ./internal/domain/listdom 2>&1 | grep escapes
```

Useful only after a heap profile points at a specific function. Don't contort code to avoid every escape.

**Profile-guided optimisation** (Go 1.21+): drop a CPU profile from production as `default.pgo` in the `main` package directory (`cmd/api/default.pgo`) and `go build` uses it automatically. Typical gain is 2–14% CPU for zero code change. Refresh the profile every few releases.

## Load testing — four shapes

| Shape | Pattern | What it tests |
|---|---|---|
| **Sustained** | 1x for 5–30 min | Baseline at expected load |
| **Spike** | 1x → 10x for 30 s → 1x | Recovery from surges |
| **Soak** | 1x for hours | Leaks — goroutines, FDs, connections, memory |
| **Ramp** | 1x → 2x → 4x → 8x → break | Where's the cliff? |

```sh
# vegeta: constant rate, latency histogram
echo "POST http://localhost:8080/lists" | \
  vegeta attack -rate=200 -duration=60s -body=list.json | vegeta report

# k6: scripted scenarios and stages (spike, ramp) in JavaScript
k6 run load.js
```

Most teams only run sustained. **Sustained doesn't predict burst behavior and doesn't catch leaks.** The cheapest upgrade: report **p99 latency and error rate**, not just "it didn't fall over."

## The failure modes that bite Go in production

### 1. Database pool sizing and growth

`*sql.DB` is a pool, not a connection. The defaults are unbounded open connections and two idle ones — fine on a laptop, a thundering herd against Postgres in production.

```go
db.SetMaxOpenConns(25)                  // below the DB's max_connections ÷ replicas
db.SetMaxIdleConns(25)                  // avoid churn under steady load
db.SetConnMaxLifetime(30 * time.Minute) // play nicely with proxies and failovers
```

Watch `db.Stats()` — `WaitCount` and `WaitDuration` rising means requests are queueing for a connection. Separately, plan for table growth: run `EXPLAIN ANALYZE` on your top queries against 10x data in staging; plans flip at scale.

### 2. Resource leaks — "forgot to close stuff"

The Go-specific classics:

```go
resp, err := http.Get(url)
if err != nil {
	return err
}
defer resp.Body.Close() // forget this and the connection is never reused

rows, err := db.QueryContext(ctx, q)
if err != nil {
	return err
}
defer rows.Close() // forget this and the pool slowly drains
for rows.Next() { /* ... */ }
if err := rows.Err(); err != nil { // forget this and errors vanish
	return err
}
```

The rule is the same as anywhere: **lifecycle ownership must be explicit.** Whoever opens it, `defer`s the close on the next line. The unit of work exists partly for this — it owns the transaction so services can't leave one open.

### 3. Goroutine leaks

A goroutine blocked forever on a channel nobody reads, or a `select` with no `ctx.Done()` case, never gets collected. Symptoms: `runtime.NumGoroutine()` climbing over days, memory creeping with it.

- Every goroutine you start needs a way to stop — usually `ctx.Done()`.
- Prefer `golang.org/x/sync/errgroup` over bare `go` for fan-out; it waits and propagates the first error.
- In tests, `go.uber.org/goleak` fails a test that leaves goroutines behind: `defer goleak.VerifyNone(t)`.
- In prod, graph the goroutine count. A slope is a leak.

### 4. Missing timeouts

`http.Get` and the zero-value `http.Client{}` have **no timeout**. One hung upstream ties up a goroutine and a connection forever.

```go
client := &http.Client{Timeout: 10 * time.Second}

srv := &http.Server{
	ReadHeaderTimeout: 5 * time.Second, // see cmd/api/main.go
	ReadTimeout:       30 * time.Second,
	WriteTimeout:      30 * time.Second,
	IdleTimeout:       120 * time.Second,
}
```

And pass `ctx` everywhere: `db.QueryContext`, `http.NewRequestWithContext`. When the client disconnects, the request's context is cancelled and the work stops.

### 5. Container limits: GOMAXPROCS and GOMEMLIMIT

- **GOMAXPROCS** — before Go 1.25 the runtime used the host's CPU count, not the container's CPU quota, which caused heavy throttling on Kubernetes. Go 1.25+ respects cgroup CPU limits by default. On older versions, use `go.uber.org/automaxprocs`.
- **GOMEMLIMIT** — a soft memory limit for the GC. Set it to ~90% of the container's memory limit (`GOMEMLIMIT=900MiB` for a 1 GiB pod) so the GC works harder before the OOM killer acts.

### 6. Spikes

Real traffic isn't uniform. Add a spike test, rate-limit at the edge (`golang.org/x/time/rate`), and shed load with a fast 503 rather than queueing unbounded work.

## The architecture connection

Clean layering makes performance work dramatically easier. With ports and adapters, a trace reads like the code:

```
POST /lists                       820ms
└─ listsvc.CreateList                 810ms
   ├─ uow.Do                       30ms
   │  └─ postgres INSERT lists     12ms
   └─ summarizer.Summarize        780ms   ← obvious where time went
```

Tangled handlers produce one opaque span. **Architecture is observability.**

## What NOT to do (the traps)

1. **Microbenchmark obsession.** `strings.Builder` vs `+` almost never matters in a backend. The DB does.
2. **`sync.Pool` and unsafe tricks without a profile.** They add bugs and complexity; only a heap profile justifies them.
3. **Caching without a hit rate.** A cache at 5% hits is slower than no cache. Measure.
4. **Goroutine-per-everything.** Concurrency isn't free parallelism; unbounded fan-out against a DB pool of 25 just queues. Bound it with `errgroup.SetLimit`.
5. **Optimising the path nobody hits.** Profile real traffic.
6. **Dashboards nobody watches.** Alert or don't bother.

## The pragmatic upgrade path

| Signal | What to add |
|---|---|
| Shipping an MVP | Timeouts on every server and client, pool limits, sustained load test with p99 |
| Occasional slowness, averages look fine | p99 dashboards, tracing |
| Launch traffic crashes things | Spike test, rate limiting, load shedding |
| Memory or goroutines creep over days | Soak test, goroutine gauge, `goleak` in tests, `GOMEMLIMIT` |
| CPU throttling in Kubernetes | Check GOMAXPROCS (Go < 1.25) |
| A hot function shows up in pprof | Benchmark + benchstat, then optimise |
| Steady CPU cost you'd like to trim | PGO with a production profile |
| Queries that were fast last month are slow | `EXPLAIN ANALYZE`, `pg_stat_statements` |

## Tools landscape (Go-specific)

| Job | Tool | Notes |
|---|---|---|
| CPU / heap / block / mutex profiling | `pprof` (`runtime/pprof`, `net/http/pprof`) | Built in. `go tool pprof -http=:0` for flame graphs. |
| Scheduler / GC / blocking analysis | `runtime/trace`, `go tool trace` | Built in. For "slow but idle". |
| Benchmarks | `testing.B`, `b.Loop()`, `-benchmem` | Built in. |
| Benchmark comparison | `benchstat` (`golang.org/x/perf`) | Statistical before/after |
| Escape analysis | `go build -gcflags=-m` | Built in |
| Profile-guided optimisation | `default.pgo` | Built in since 1.21 |
| Load testing | k6, vegeta, hey | k6 for scenarios, vegeta for constant-rate |
| Goroutine leak detection | `go.uber.org/goleak` | In tests |
| Continuous profiling | Pyroscope, Datadog, Parca | pprof in prod, always on |
| Tracing | OpenTelemetry Go | See [instrumentation.md](./instrumentation.md) |
| DB analysis | `EXPLAIN ANALYZE`, `pg_stat_statements`, `sql.DBStats` | Worth more than any third-party tool |

## Self-check

1. Your endpoint takes 800ms and CPU is at 5%. pprof, tracing, or `go tool trace` — which first, and why?
2. Why does a zero-value `http.Client` worry you?
3. What does `db.Stats().WaitCount` rising tell you?
4. `runtime.NumGoroutine()` grows by 1,000 a day. Name two likely causes.
5. Why is a single `go test -bench` run not evidence of a speedup?
6. What changed about GOMAXPROCS in Go 1.25, and why did it matter in containers?

## Things to steal

- **Five tools, five questions.** Tracing for "where", pprof for "what code", `go tool trace` for "why idle".
- **p99, not p50.** Tails reveal GC, contention and pool exhaustion.
- **Timeouts on every server, every client, every context.** No exceptions.
- **`defer Close()` on the line after open.** `resp.Body`, `rows`, files, transactions.
- **Every goroutine needs a way to stop.** And a gauge that shows if they don't.
- **Size the `sql.DB` pool deliberately.** The defaults are wrong for production.
- **benchstat or it didn't happen.**
- **Architecture is observability.** Clean spans come from clean layers.
- **Don't optimise without measuring. Don't measure without acting.**
