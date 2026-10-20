# Logging & Observability

← [Back to index](./README.md)

Logging is easy to do badly for years before you notice. The good news in Go: since 1.21 the standard library ships `log/slog`, a structured logger good enough that most services need nothing else. This doc covers the few concepts that matter and the traps.

## The goal (the only one)

> **When something goes wrong in prod, you should be able to answer "what happened to *this* user's request?" in under 60 seconds.**

Coverage of every line, fancy formatters, log volume — none of it matters if you can't answer that. A small amount of *structured* logging plus a request ID lets you answer it cheaply.

## The recommended stack

| Layer | Tool | Job |
|---|---|---|
| **Structured app logs** | `log/slog` (stdlib) | Business events, request boundaries, errors |
| **Request ID** | ~20 lines of your own middleware + a `slog.Handler` wrapper | One ID on every log line for a request |
| **Tracing / spans** | OpenTelemetry Go (`otelhttp`, `otelsql`) | Where did the time go? See [instrumentation.md](./instrumentation.md) |

No logging library to choose. `zap` and `zerolog` predate `slog` and are still faster in microbenchmarks; unless a profile says logging is your bottleneck, the stdlib wins on zero dependencies and a shared ecosystem — every `slog.Handler` works with every `*slog.Logger`.

## Key concepts

### 1. Structured logging — key-value, not Sprintf

```go
// Bad — grep-able only
log.Printf("user %s created list %s with slug %s", owner, l.ID, l.Slug)

// Good — queryable
logger.InfoContext(ctx, "list created", "owner", owner, "list_id", l.ID, "slug", l.Slug)
```

The good version lets you query "all `list created` events for `owner=u1` in the last hour". The bad version needs grep.

Rules of thumb:
- **The message is a constant**, short and lowercase: `"list created"`, `"payment failed"`. Never build it with `fmt.Sprintf`.
- **Data goes in attributes**, never in the message.
- **Same message = same shape.** `list created` always has `owner` and `list_id`, never `user` in one place and `owner` in another.
- Key names are `snake_case` and stable — they become query fields in your aggregator.

For hot paths or when you want the compiler to catch mismatched pairs, use typed attributes: `logger.LogAttrs(ctx, slog.LevelInfo, "list created", slog.String("list_id", string(l.ID)))`. The `sloglint` linter can enforce one style.

### 2. JSON in prod, text in dev

```go
func newLogger(env string) *slog.Logger {
	var h slog.Handler
	if env == "dev" {
		h = slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug})
	} else {
		h = slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})
	}
	return slog.New(contextHandler{h})
}
```

[`cmd/api/main.go`](../../../../example/cmd/api/main.go) builds the logger once, in `main`, and passes it down.

### 3. Inject the logger; don't reach for a global

The example passes `*slog.Logger` into `rest.NewHandler`. That's the same dependency-injection rule as everything else: `main` builds it, everything below receives it. Tests pass `slog.New(slog.DiscardHandler)` (Go 1.24) and stay quiet.

The pragmatic alternative is `slog.SetDefault(logger)` in `main` and `slog.InfoContext(...)` elsewhere. It's fine for small services, but it's a hidden global: tests can't capture or silence logs per test, and parallel tests share it. Pick injection for anything with more than one package that logs.

Use `logger.With("component", "rest")` to derive a child logger with fixed attributes rather than repeating them on every call.

### 4. Request IDs ride on context.Context

Go has no implicit request-scoped state. The request ID goes into the `context.Context`, and a small `slog.Handler` wrapper copies it onto every record. Because the handler reads from `ctx`, you must use the `*Context` methods (`InfoContext`, `ErrorContext`) — plain `Info` passes `context.Background()` and the ID is silently lost.

```go
package rest

import (
	"context"
	"crypto/rand"
	"log/slog"
	"net/http"
)

type requestIDKey struct{}

// RequestID reads or assigns X-Request-ID and stores it on the context.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-ID")
		if id == "" {
			id = rand.Text()
		}
		w.Header().Set("X-Request-ID", id)
		ctx := context.WithValue(r.Context(), requestIDKey{}, id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// contextHandler adds request-scoped attributes from ctx to every record.
type contextHandler struct{ slog.Handler }

func (h contextHandler) Handle(ctx context.Context, r slog.Record) error {
	if id, ok := ctx.Value(requestIDKey{}).(string); ok {
		r.AddAttrs(slog.String("request_id", id))
	}
	return h.Handler.Handle(ctx, r)
}

func (h contextHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return contextHandler{h.Handler.WithAttrs(attrs)}
}

func (h contextHandler) WithGroup(name string) slog.Handler {
	return contextHandler{h.Handler.WithGroup(name)}
}
```

`WithAttrs` and `WithGroup` must re-wrap, or `logger.With(...)` silently drops the wrapper. If you run OpenTelemetry, add `trace_id` and `span_id` from `trace.SpanContextFromContext(ctx)` in the same `Handle` method — then logs and traces join on one ID.

Output:

```json
{"time":"…","level":"INFO","msg":"list created","owner":"u1","list_id":"l1","request_id":"7QKX…"}
{"time":"…","level":"INFO","msg":"request completed","method":"POST","path":"/lists","status":201,"duration":"1.2ms","request_id":"7QKX…"}
```

### 5. Log request boundaries in middleware

```go
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func AccessLog(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		log.InfoContext(r.Context(), "request completed",
			"method", r.Method, "path", r.Pattern, "status", rec.status,
			"duration", time.Since(start))
	})
}
```

Wire it as `RequestID(AccessLog(log, mux))` so the access log sees the ID. `r.Pattern` (Go 1.23) is the matched route like `GET /lists/{id}` — low-cardinality and better for grouping than the raw path.

### 6. Background work needs the context, not the request's cancellation

A goroutine started from a handler and given `context.Background()` loses the request ID. Given `r.Context()` directly, it gets cancelled the moment the response is written. Use `context.WithoutCancel` (Go 1.21): keeps the values, drops the cancellation.

```go
bg := context.WithoutCancel(r.Context())
go func() {
	if err := notifier.Send(bg, msg); err != nil {
		log.ErrorContext(bg, "notification failed", "err", err) // still has request_id
	}
}()
```

(And track that goroutine — see goroutine leaks in [performance.md](./performance.md).)

## Logs vs. traces — the distinction

| | Logs | Traces |
|---|---|---|
| **Question answered** | "what happened?" | "where did the time go?" |
| **Shape** | Discrete events with attributes | Nested spans with durations |
| **Always emitted?** | Yes | Often sampled |
| **Best for** | Business events, errors, audit | Latency, request flow |
| **Tool** | `slog` → JSON → aggregator | OpenTelemetry → Jaeger/Tempo/Datadog |

You want both, joined by `trace_id` on the log line.

## What to log vs. not log

**Always log:**
- Request boundary: method, route pattern, status, duration, request ID
- Business events: `list created`, `subscription upgraded`, `payment charged`
- Unexpected errors, once, with context (which entity, which input shape)
- Slow external calls you don't control

**Never log:**
- Passwords, API keys, tokens, session cookies, `Authorization` headers
- Full request/response bodies containing PII
- Card numbers, even partial

**Redact by type, not by discipline.** Implement `slog.LogValuer` on secret-bearing types so they can't leak even when someone logs the whole struct:

```go
type APIKey string

func (APIKey) LogValue() slog.Value { return slog.StringValue("[REDACTED]") }

type Credentials struct {
	User string
	Key  APIKey
}
// logger.Info("auth", "creds", creds) → creds={User:u1 Key:[REDACTED]}
```

The pragmatic rule: **if it would embarrass you in a data breach, don't log it.**

## Levels

slog has four: `Debug`, `Info`, `Warn`, `Error`.

- `Debug` — only useful while chasing a specific issue. Off in prod.
- `Info` — normal operation: business events, request boundaries.
- `Warn` — unexpected but handled: a retry succeeded, a fallback was used.
- `Error` — something is broken and someone should look.

A `listdom.ErrNotFound` is **not** an error log — it's an expected 404. In the example's [`rest.fail`](../../../../example/internal/delivery/rest/rest.go), domain errors map to 4xx silently; only the `default` branch (truly unexpected) logs at `Error`.

Use `slog.LevelVar` if you want to flip the level at runtime without a restart.

## The traps

### Trap 1: Sprintf logs pretending to be structured

```go
logger.Info(fmt.Sprintf("created list %s for %s", id, owner))
```

One unqueryable string. Move the values to attributes.

### Trap 2: same message, different shapes

```go
// in app
logger.InfoContext(ctx, "list created", "owner", owner, "list_id", id)
// in admin
logger.InfoContext(ctx, "list created", "admin", a, "target_user", owner, "list", id)
```

Your dashboard now needs two queries. Same message = same shape; if the shape differs, it's a different event — name it differently.

### Trap 3: log-and-return

```go
l, err := r.Lists.Get(ctx, id)
if err != nil {
	logger.ErrorContext(ctx, "get failed", "err", err) // logged here…
	return err                                        // …and again by every caller
}
```

The same failure appears four times at four levels. **Either handle the error or return it — not both.** Wrap with context on the way up (`fmt.Errorf("add item to %s: %w", id, err)`), and log once at the boundary that decides the outcome (the HTTP handler, the job runner).

### Trap 4: dropping the context

`logger.Info(...)` instead of `logger.InfoContext(ctx, ...)` throws away the request ID and trace ID. Make the `Context` variants the only ones you use outside `main`; `sloglint` has a rule for it.

### Trap 5: logging at the wrong level

`Error` for a missing list, `Info` inside a hot loop. `Error` should mean someone needs to look; `Info` should be affordable at peak traffic.

### Trap 6: logging too much

Every line costs storage and indexing. Log at the boundary of work, not per item inside a loop. Sample debug logs in hot paths.

### Trap 7: secrets in error logs

`logger.Error("call failed", "req", req)` where `req` has an `Authorization` header. Use `LogValuer` on the secret types and log the fields you need, not whole request structs.

### Trap 8: `fmt.Println`, `log.Printf`, `log.Fatal` outside main

`fmt.Println` has no level, no JSON, no request ID. The old `log` package has no structure. `log.Fatal` calls `os.Exit(1)` — deferred functions don't run, so connections, files and buffered logs are lost, and library code that calls it takes the whole process down. Return errors; only `main` decides to exit (see `run()` in [`cmd/api/main.go`](../../../../example/cmd/api/main.go)).

### Trap 9: no request boundary logs

If you only log inside services, a request that fails in routing or decoding is invisible. The access-log middleware guarantees every request leaves one line.

## How to spot it in your code

```sh
grep -rn 'fmt.Print\|log.Print\|log.Fatal' --include=*.go internal/
grep -rn 'Sprintf' --include=*.go internal/ | grep -i 'log\|slog'
grep -rnE '\.(Info|Warn|Error|Debug)\(' --include=*.go internal/   # missing Context variant
```

| Symptom | Fix |
|---|---|
| `fmt.Println` / `log.Printf` in `internal/` | Inject `*slog.Logger` |
| Values formatted into the message | Move to attributes |
| `log.Fatal` outside `main` | Return the error |
| The same error logged at several layers | Wrap and return; log once at the boundary |
| No `request_id` on log lines | RequestID middleware + context handler + `*Context` calls |
| Can't silence logs in tests | Stop using the global; inject `slog.DiscardHandler` |

## Pragmatic upgrade path

| Stage | What to add | Effort |
|---|---|---|
| 1 | `slog` JSON handler built in `main`, injected down | 15 min |
| 2 | Access-log middleware (method, pattern, status, duration) | 20 min |
| 3 | Request-ID middleware + `contextHandler` | 20 min |
| 4 | Replace `fmt`/`log` calls; remove log-and-return | 1–2 h |
| 5 | `LogValuer` on secret types | 15 min |
| 6 | OpenTelemetry tracing; add `trace_id` to the handler | 1 h |
| 7 | Runtime level via `slog.LevelVar`; sample hot-path debug logs | 30 min |

Stages 1–3 are enough for a small service.

## Self-check

1. Why is `logger.Info(fmt.Sprintf(...))` worse than attributes?
2. Your handler wrapper adds `request_id`, but some lines don't have it. What's the most likely cause?
3. Why must `WithAttrs` and `WithGroup` re-wrap in a custom handler?
4. What's wrong with logging an error and then returning it?
5. When is `context.WithoutCancel` the right context for a goroutine?
6. You see `list not found` at level ERROR. Right or wrong?
7. Why is `log.Fatal` in a library package dangerous?

## Things to steal

- **`log/slog` is enough.** Stdlib, structured, pluggable handlers.
- **Constant messages, data in attributes.** Same message = same shape.
- **Build the logger in `main`, inject it down.** Tests get `slog.DiscardHandler`.
- **Request ID on the context, copied by a handler wrapper.** Always call the `*Context` methods.
- **Log or return, never both.** Wrap on the way up; log once where the outcome is decided.
- **`LogValuer` for secrets.** Redaction by type beats redaction by memory.
- **Only `main` exits.** No `log.Fatal`, no `os.Exit` in `internal/`.
- **Logs say what happened; traces say where the time went.** Join them on `trace_id`.
- **If it would embarrass you in a data breach, don't log it.**
