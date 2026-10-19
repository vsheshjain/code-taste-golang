# Code-Level Instrumentation

← [Back to index](./README.md)

What to wire *into* your code for measurement: spans, health checks, metrics, benchmarks. Companion to [performance.md](./performance.md), which covers the *external* tooling.

## The goal (the only one)

> **Auto-instrumentation tells you which SQL query was slow. Code-level instrumentation tells you which *use case* was slow. You want both, but most teams stop after auto-instrumentation and miss the second half.**

The right amount: enough that a slow request's trace reads like a story ("request → listsvc.AddItem → load list → save list"), not so much that every function call is a span.

## The default rule — don't instrument what's already free

Go has no runtime monkey-patching, so "auto" instrumentation means *wrapping* the transports once at the composition root (`cmd/api/main.go`). After that, every call through them is traced for free.

| What | Free once you wrap… | Add code elsewhere? |
|---|---|---|
| Inbound HTTP duration, status, route | `otelhttp.NewHandler(mux, "api")` | No |
| Outbound HTTP calls | `&http.Client{Transport: otelhttp.NewTransport(http.DefaultTransport)}` | No |
| Every SQL query | `otelsql.Open(...)` instead of `sql.Open(...)` | No |
| gRPC | `otelgrpc` stats handler | No |
| CPU / heap profiling | `net/http/pprof` on a debug port | No |
| Go runtime metrics (GC, goroutines, memory) | `go.opentelemetry.io/contrib/instrumentation/runtime` or Prometheus' Go collector | No |

```go
// cmd/api/main.go — the only place that knows about otel* wrappers
db, err := otelsql.Open("pgx", dsn, otelsql.WithAttributes(semconv.DBSystemPostgreSQL))
if err != nil {
	return err
}
srv := &http.Server{
	Addr:    addr,
	Handler: otelhttp.NewHandler(httpapi.NewHandler(svc, log), "api"),
}
```

Wrapping at the edge keeps the domain and the service free of telemetry imports for everything except the custom spans below. **Don't add a span around a `db.QueryContext` call** — `otelsql` already emits one.

## The five things worth wiring in

Add 1–2 first; the rest only for a specific reason.

### 1. Custom spans for use cases

The highest-leverage change. Auto-instrumentation shows you primitives (HTTP in, SQL, HTTP out); *you* know which use case they belong to.

```go
package listsvc

import (
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

var tracer = otel.Tracer("github.com/vsheshjain/code-taste-golang/example/internal/service/listsvc")

// AddItem appends an item to a list: load, delegate, save.
func (s *Service) AddItem(ctx context.Context, id listdom.ID, it listdom.Item) (err error) {
	ctx, span := tracer.Start(ctx, "listsvc.AddItem",
		trace.WithAttributes(attribute.String("list.id", string(id))))
	defer func() {
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		}
		span.End()
	}()

	// Body unchanged. The SQL spans nest under listsvc.AddItem because ctx now carries the span.
	return s.uow.Do(ctx, func(r Repos) error {
		l, err := r.Lists.Get(ctx, id)
		if err != nil {
			return fmt.Errorf("add item to %s: %w", id, err)
		}
		if err := l.AddItem(it); err != nil {
			return err
		}
		return r.Lists.Save(ctx, l)
	})
}
```

Three Go-specific points:

- **`ctx, span := tracer.Start(ctx, ...)` — reassign `ctx`.** If you keep the old context, child spans attach to the parent instead of this one and the tree is wrong.
- **`defer span.End()` on the next line.** A span that never ends is never exported.
- **The tracer is a package-level variable obtained from the global provider.** `main` installs the real provider with `otel.SetTracerProvider`; until it does (including in tests), calls are no-ops. That's the one sanctioned global in this architecture.

**Where to add spans:** each service method (one span per use case, named `listsvc.CreateList`, `listsvc.AddItem`), and multi-phase background jobs.

**Where not to:** domain methods (pure, microseconds), inside loops, or around calls already instrumented by `otelhttp`/`otelsql`.

Should `internal/service/listsvc` import OpenTelemetry at all? It's the pragmatic default: the OTel API is a thin, stable, vendor-neutral interface with a no-op default. If you want the service package dependency-free, a decorator in `cmd/api` that wraps `*listsvc.Service` works too — but it duplicates every method signature. Most teams accept the import.

### 2. Real health endpoints: /healthz and /readyz

A health check that always returns 200 is theatre. Split liveness from readiness:

- **`/healthz`** (liveness) — is the process alive? Returns 200 unless the process is wedged. Never checks dependencies; if it did, a DB blip would make Kubernetes restart every pod at once.
- **`/readyz`** (readiness) — should this instance get traffic? Pings real dependencies with a short timeout.

```go
// Check is one dependency probe.
type Check func(ctx context.Context) error

func Health(checks map[string]Check) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()

		status, results := http.StatusOK, map[string]string{}
		for name, check := range checks {
			if err := check(ctx); err != nil {
				status, results[name] = http.StatusServiceUnavailable, "down: "+err.Error()
				continue
			}
			results[name] = "ok"
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(results)
	})
	return mux
}

// in main:
// Health(map[string]Check{"postgres": db.PingContext})
```

`db.PingContext` already has the `Check` signature — no adapter needed. During graceful shutdown, flip readiness to 503 *before* calling `srv.Shutdown` so the load balancer drains you first.

### 3. Custom metrics — only when you've named the dashboard

Counters, histograms, gauges. Three options, smallest first:

**`expvar` (stdlib, zero dependencies)** — exposes JSON at `/debug/vars`:

```go
var listsCreated = expvar.NewInt("lists_created")

// in the use case, after commit:
listsCreated.Add(1)

// on the debug mux:
mux.Handle("GET /debug/vars", expvar.Handler())
```

Good enough for a single instance or a quick look. No percentiles, no labels.

**OpenTelemetry metrics** — same SDK as tracing, exports to Prometheus, OTLP, Datadog:

```go
var meter = otel.Meter("github.com/vsheshjain/code-taste-golang/example/internal/service/listsvc")

var listsCreated, _ = meter.Int64Counter("lists.created",
	metric.WithDescription("Lists successfully created"))

listsCreated.Add(ctx, 1)
```

**Prometheus `client_golang`** — if Prometheus is already your stack:

```go
var listsCreated = promauto.NewCounter(prometheus.CounterOpts{
	Name: "lists_created_total",
	Help: "Lists successfully created.",
})

listsCreated.Inc()
// main: mux.Handle("GET /metrics", promhttp.Handler())
```

Use cases:
- **Counter** — events you want a rate of (lists created, payments failed)
- **Histogram** — distributions you want percentiles of (latency, payload size, tokens used)
- **Gauge** — current values (queue depth, pool in use, active sessions)

Request rate, latency and error rate per route are already free from `otelhttp`. Don't add them by hand.

### 4. Benchmarks for hot functions

For CPU-bound functions where a regression matters — a parser, a slugger, an encoder. Lives next to the code ([`list_test.go`](../../../../example/internal/domain/listdom/list_test.go)):

```go
func BenchmarkSlugify(b *testing.B) {
	for b.Loop() {
		listdom.Slugify("A Fairly Long List Name With Some Punctuation!")
	}
}
```

Run with `go test -run=^$ -bench=. -benchmem ./internal/domain/listdom`; compare runs with `benchstat` (see [performance.md](./performance.md)). Not for code that does I/O — network variance swamps the signal.

### 5. Latency budget assertions in tests

Rare and somewhat brittle (CI machines vary), but it catches "this got 5x slower and nobody noticed" on flows where latency is part of the contract:

```go
func TestCreateListLatencyBudget(t *testing.T) {
	if testing.Short() {
		t.Skip("latency budget test")
	}
	svc := listsvc.New(&listsvctest.Store{}, func() listdom.ID { return listdom.ID(rand.Text()) })
	srv := httptest.NewServer(httpapi.NewHandler(svc, slog.New(slog.DiscardHandler)))
	defer srv.Close()

	durations := make([]time.Duration, 200)
	for i := range durations {
		start := time.Now()
		resp, err := http.Post(srv.URL+"/lists", "application/json",
			strings.NewReader(`{"owner":"u1","name":"Budget"}`))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		durations[i] = time.Since(start)
	}
	slices.Sort(durations)
	if p99 := durations[len(durations)*99/100]; p99 > 20*time.Millisecond {
		t.Errorf("p99 = %v, budget 20ms", p99)
	}
}
```

Keep the budget generous (several times the observed p99) so it only fires on real regressions.

## Worked example — instrumenting a use case end to end

Assumes the `Service` from the example module gains a `log *slog.Logger` field, injected through `listsvc.New` like everything else.

```go
func (s *Service) CreateList(ctx context.Context, owner, name string) (_ *listdom.List, err error) {
	ctx, span := tracer.Start(ctx, "listsvc.CreateList", trace.WithAttributes(attribute.String("owner", owner)))
	defer func() {
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		}
		span.End()
	}()

	l, err := listdom.New(s.newID(), owner, name)
	if err != nil {
		return nil, err
	}
	if err := s.uow.Do(ctx, func(r Repos) error { return r.Lists.Save(ctx, l) }); err != nil {
		return nil, fmt.Errorf("create list: %w", err)
	}
	span.SetAttributes(attribute.String("list.id", string(l.ID)))
	listsCreated.Add(ctx, 1)
	s.log.InfoContext(ctx, "list created", "owner", owner, "list_id", l.ID)
	return l, nil
}
```

What you get:
- A span `listsvc.CreateList` with `owner` and `list.id`, marked as error on failure
- Child spans (free, from `otelsql`): `BEGIN`, `INSERT`, `COMMIT`
- A parent span (free, from `otelhttp`): `POST /lists`
- A counter for the dashboard
- One structured log line carrying `request_id` and `trace_id`

About six lines of instrumentation for one use case. That's the right ratio. If you find the span boilerplate repetitive across many methods, a tiny helper (`func end(span trace.Span, err *error)`) is fine — a code generator is not.

## The traps

### Trap 1: span explosion

```go
for _, it := range items {
	_, span := tracer.Start(ctx, "process item") // 1,000 items = 1,000 spans
	process(it)
	span.End()
}
```

Span the loop, not the iteration; put the count on the span: `attribute.Int("items", len(items))`.

### Trap 2: spans around already-instrumented calls

A manual `"query lists"` span around `db.QueryContext` when `otelsql` is installed doubles trace volume for zero information.

### Trap 3: forgetting to reassign ctx

```go
_, span := tracer.Start(ctx, "listsvc.AddItem") // ctx not updated
defer span.End()
s.uow.Do(ctx, ...)                          // SQL spans attach to the HTTP span, not this one
```

The trace looks flat and the use-case span looks instant. Always `ctx, span := tracer.Start(ctx, ...)`.

### Trap 4: metrics nobody dashboards

A counter that exists in code but in no dashboard or alert is debt. Name the dashboard before adding the metric.

### Trap 5: high-cardinality labels

```go
listsCreated.Add(ctx, 1, metric.WithAttributes(attribute.String("owner", owner))) // one series per user
```

Every distinct label value is a separate time series — per-user labels blow up your metrics bill and backend. Low-cardinality values (status, plan, region, route pattern) go on metrics; high-cardinality IDs (`owner`, `list.id`, `request_id`) go on **spans and logs**. Same reason the access log uses `r.Pattern`, not `r.URL.Path`.

### Trap 6: hand-rolled timing instead of tracing

```go
start := time.Now()
defer func() { log.Printf("AddItem took %v", time.Since(start)) }()
```

Scattered timing logs are a sign tracing is missing. Install `otelhttp`/`otelsql` and one span per use case; delete the timers. Benchmarks are the exception, for in-memory hot paths.

### Trap 7: instrumenting before measuring

Spans everywhere "just in case" is busywork with a storage bill. Add instrumentation where a slow report came in, where traces are sparse around an operation, or before a refactor you want a baseline for.

### Trap 8: exposing debug endpoints publicly

`/debug/pprof` and `/debug/vars` leak internals (and pprof can be used to burn CPU). Serve them on a separate listener bound to localhost or an internal network — never on the public mux.

## How to spot where spans would help

Open your tracing UI, take the slowest 10% of requests, and look:

| Symptom | What to add |
|---|---|
| A flat list of SQL and HTTP spans under the request | One span per use case in `internal/service/listsvc` |
| Total time visible, but not which phase | Child spans for each phase of the workflow |
| Slow, but can't tell for which entity | Add the ID as a span attribute |
| Use-case span is ~0ms but the request is slow | You didn't reassign `ctx` |
| `/readyz` says OK during a DB outage | Add the real dependency checks |
| Want "lists created per hour" | Add a counter |
| A pure function is suspect | Benchmark it |

## Pragmatic order

For a new service: wrap `otelhttp` + `otelsql` in `main`, add `/healthz` and `/readyz`. Nothing else yet.

| Stage | What to add |
|---|---|
| 1 | `otelhttp`, `otelsql`, `otelhttp.NewTransport` at the composition root |
| 2 | `/healthz` + `/readyz` with real dependency checks |
| 3 | One span per service method |
| 4 | Counters for the top business events (signups, payments) |
| 5 | Latency budget tests on user-critical flows |
| 6 | Benchmarks for any function that's been a regression source |

Stages 1–3 cover most of the value.

## Self-check

1. Why is a manual span around `db.QueryContext` usually wrong?
2. What breaks if you write `_, span := tracer.Start(ctx, ...)`?
3. Why must `/healthz` not check the database?
4. Counter, histogram, gauge — which for "LLM tokens per request", and why?
5. Why is `owner` fine on a span but wrong as a metric label?
6. When is `expvar` enough, and when do you need OTel or Prometheus?

## Things to steal

- **Wrap the transports once, in `main`.** `otelhttp` + `otelsql` give you 80% for free.
- **One span per service method.** Named after the use case, IDs as attributes.
- **`ctx, span := tracer.Start(ctx, ...)` then `defer span.End()`.** Every time.
- **Liveness is about the process; readiness is about dependencies.**
- **IDs on spans and logs, never on metric labels.**
- **No metric without a named dashboard.**
- **Debug endpoints on a private listener.**
- **Benchmarks for CPU hot paths only.** Not a substitute for load tests.
- **About six lines of instrumentation per use case.** More is noise; less and traces don't read.
