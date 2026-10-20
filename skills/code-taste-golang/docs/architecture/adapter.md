# Adapter (and Port)

← [Back to index](./README.md)

## One-line definition

An adapter is **a swappable seam between your code and an external system**, and a port is **the interface that defines the seam**.

If you understood Repository, you've already used the pattern. Adapter is the generalisation: *not every external system is a database.*

## Port and adapter: what each word means in Go

| Term | What it is in Go | Example |
|---|---|---|
| **Port** | An interface **declared in the service that consumes it**, in that service's `ports.go`. Small, often one or two methods. | `listsvc.Store`, `listsvc.Summarizer`, `billingsvc.Payments` |
| **Adapter** | A concrete struct that satisfies the port implicitly. Storage adapters live in `internal/repo/<name>repo`; every other external system in `internal/gateway/<vendor>gw`, named after the vendor plus `gw`. | `listrepo.Lists`, `openaigw.Summarizer`, `stripegw.Client` |
| **Fake** | A hand-written, exported struct in a `<svc>test` package beside the port, `httptest`-style. Imported only from `_test.go` files. | `listsvctest.Store`, `listsvctest.FakeSummarizer` |

Mental model: the **port is the shape of the hole**, the **adapter is the plug**. The service only knows the hole.

Why two adapter folders? `repo/` holds data the app owns: its schema, its migrations, its transactions. `gateway/` (named for Fowler's Gateway pattern: an object that wraps access to an external system) holds systems someone else owns (an EMR vendor, an LLM provider, a payment gateway) that the app calls over the network. They fail differently, are tested differently, and the split tells a reader at a glance which is which.

Go makes this cheaper than most languages because satisfaction is implicit. The adapter doesn't name the interface, doesn't embed a base class, and often doesn't import the consumer at all. Any type with the right methods fits. This is why Go puts interfaces on the consumer side: the consumer knows exactly which methods it needs, and the implementer doesn't have to guess ([Code Review Comments: Interfaces](https://go.dev/wiki/CodeReviewComments#interfaces)).

You'll also hear this called *hexagonal architecture* or *ports and adapters*. Same idea.

## The problem it solves

If the service calls an SDK directly:

1. **You can't test the use case without the external system.** Testing "cancel the subscription when the charge fails" needs a Stripe test account and the network.
2. **Swapping providers is a rewrite.** Every file that imports the OpenAI SDK breaks when you move to another provider.
3. **SDK types leak inward.** `*stripe.Customer` and `openai.ChatCompletionResponse` start appearing in service signatures, and the SDK's version bumps become your refactors.

One port per external system, one adapter per technology, fixes all three.

## Repository is just one specific adapter

```
listsvc.Store (interface, in internal/service/listsvc/ports.go)   ← port
    ↑ satisfied implicitly by
    ├── *listrepo.Lists   (internal/repo/listrepo)                 ← production (Postgres)
    ├── *listrepo.File    (internal/repo/listrepo)                 ← the example's JSON file
    └── listsvctest.Store (internal/service/listsvc/listsvctest)   ← fake for tests
```

Same shape for an LLM:

```
listsvc.Summarizer
    ├── *openaigw.Summarizer         (internal/gateway/openaigw)
    ├── *anthropicgw.Summarizer      (internal/gateway/anthropicgw)
    └── *listsvctest.FakeSummarizer  (beside the port)
```

And for payments:

```
billingsvc.Payments
    ├── *stripegw.Client             (internal/gateway/stripegw)
    └── *billingsvctest.FakePayments (beside the port)
```

Once you've seen one, you've seen them all.

## What it looks like

Port and DTOs, declared by the consumer in its `ports.go`. Port DTOs live right next to the interface that uses them:

```go
// internal/service/listsvc/ports.go
package listsvc

type SummaryRequest struct {
	Text     string
	MaxWords int
}

type Summary struct {
	Text       string
	TokensUsed int
}

type Summarizer interface {
	Summarize(ctx context.Context, req SummaryRequest) (Summary, error)
}

var ErrRateLimited = errors.New("summarizer rate limited")
```

The use case, in `service.go`, depends only on the port:

```go
// internal/service/listsvc/service.go
func (s *Service) Summarize(ctx context.Context, id listdom.ID) (Summary, error) {
	l, err := s.GetList(ctx, id)
	if err != nil {
		return Summary{}, err
	}
	return s.summarizer.Summarize(ctx, SummaryRequest{Text: l.AsText(), MaxWords: 50})
}
```

The real adapter, under `gateway/`, in a package named for the provider plus `gw`. The suffix keeps it from colliding with the provider's own SDK package (`github.com/openai/openai-go` is `package openai`):

```go
// internal/gateway/openaigw/summarizer.go
package openaigw

type Summarizer struct {
	client *http.Client
	apiKey string
	model  string
}

func NewSummarizer(client *http.Client, apiKey, model string) *Summarizer {
	return &Summarizer{client: client, apiKey: apiKey, model: model}
}

func (s *Summarizer) Summarize(ctx context.Context, req listsvc.SummaryRequest) (listsvc.Summary, error) {
	body, _ := json.Marshal(chatRequest{
		Model:    s.model,
		Messages: []message{{Role: "user", Content: fmt.Sprintf("Summarize in %d words: %s", req.MaxWords, req.Text)}},
	})
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.openai.com/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return listsvc.Summary{}, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+s.apiKey)
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(httpReq)
	if err != nil {
		return listsvc.Summary{}, fmt.Errorf("openai: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusTooManyRequests {
		return listsvc.Summary{}, listsvc.ErrRateLimited // translate to our vocabulary
	}
	if resp.StatusCode != http.StatusOK {
		return listsvc.Summary{}, fmt.Errorf("openai: status %d", resp.StatusCode)
	}

	var out chatResponse // unexported: the provider's shape stays here
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return listsvc.Summary{}, fmt.Errorf("openai: decode: %w", err)
	}
	return listsvc.Summary{Text: out.Choices[0].Message.Content, TokensUsed: out.Usage.TotalTokens}, nil
}
```

The fake, hand-written, in the service's test-helper package. It lives beside the port it implements, not beside `openaigw`, because it fakes *our* interface, not OpenAI's API. That's the standard library's pattern: `net/http/httptest`, `testing/fstest`, `io/iotest`.

```go
// internal/service/listsvc/listsvctest/summarizer.go
package listsvctest

type FakeSummarizer struct {
	Reply string
	Calls []listsvc.SummaryRequest
}

func (f *FakeSummarizer) Summarize(_ context.Context, req listsvc.SummaryRequest) (listsvc.Summary, error) {
	f.Calls = append(f.Calls, req)
	return listsvc.Summary{Text: f.Reply, TokensUsed: 42}, nil
}
```

Exported, so the service tests *and* the HTTP end-to-end tests can share it. A fake used by exactly one test file can stay unexported in that file; promote it to `<svc>test` the second time it's needed.

And the wiring, once, in [cmd/api/main.go](../../../../example/cmd/api/main.go):

```go
svc := listsvc.New(uow, openaigw.NewSummarizer(http.DefaultClient, cfg.OpenAIKey, "gpt-4o-mini"), newID)
```

Things to notice:

1. **The port defines its own DTOs.** `listsvc.SummaryRequest`, in `ports.go`, not the provider's request type. The provider's `chatRequest`/`chatResponse` are unexported in `openai`, so they physically can't leak.
2. **The adapter imports `listsvc`** to name the DTOs in its method signature. That's allowed: adapters point inward. `listsvc` never imports `openaigw`.
3. **Errors are translated at the seam.** A 429 becomes `listsvc.ErrRateLimited`, which the service or handler can match with `errors.Is`. Everything else is wrapped with `%w` and a prefix that names the adapter.
4. **`ctx` flows into the outgoing request** with `NewRequestWithContext`, so a cancelled HTTP request upstream cancels the LLM call.
5. **The fake is a plain struct** in `listsvctest`, not a mock framework. It records calls in a slice; the test asserts on the slice. No `EXPECT()`, no generated code. The example's [arch_test.go](../../../../example/arch_test.go) fails the build if non-test code imports a `<svc>test` package, so fakes can't leak into production wiring.

### Compile-time interface checks

Because satisfaction is implicit, a typo in a method signature only surfaces where the adapter is passed to the service. To catch it in the adapter's own package, add one line:

```go
var _ listsvc.UnitOfWork = (*File)(nil)
```

The example's [listrepo](../../../../example/internal/repo/listrepo/listrepo.go) and its [fake](../../../../example/internal/service/listsvc/listsvctest/store.go) both do exactly this. It costs nothing at runtime.

## Real examples

| External system | Port (in the service's `ports.go`) | Real adapter | Fake (in `<svc>test`) |
|---|---|---|---|
| Storage | `listsvc.Store`, `listsvc.UnitOfWork` | `repo/listrepo` (+ `repo/pgtx` for shared transactions) | `listsvctest.Store` |
| LLM | `listsvc.Summarizer` | `gateway/openaigw.Summarizer` | `listsvctest.FakeSummarizer` |
| Payments | `billingsvc.Payments` | `gateway/stripegw.Client` | `billingsvctest.FakePayments` |
| EMR vendor | `emrsvc.EHRClient` | `gateway/epicgw.Client` | `emrsvctest.FakeEHR` |
| Email | `notifysvc.Mailer` | `gateway/sesgw.Mailer` | `notifysvctest.FakeMailer` |

## What's in / what's out

**In the port:**
- The methods the use cases call, and nothing more
- DTOs in *your* shape
- Sentinel errors the consumer needs to react to (`ErrRateLimited`, `ErrCardDeclined`)

**In the adapter:**
- SDK or HTTP calls
- Mapping your DTOs to the provider's and back
- Mapping provider errors to your errors
- Provider config (API keys, base URLs, models), received through its constructor

**Out of both:**
- Business rules. The adapter doesn't decide *whether* to charge, only *how*.
- Use-case orchestration (the service)
- Reading env vars. `main` reads config and passes values into constructors.

## The traps

### Trap 1: SDK types in the port

```go
type Summarizer interface {
	Summarize(ctx context.Context, text string) (*openai.ChatCompletionResponse, error) // wrong
}
```

`listsvc` now imports the SDK, and the service unpacks provider JSON. Define your own `Summary` and let the adapter translate.

### Trap 2: the adapter that's really a service

```go
func (g *Gateway) ChargeForSubscription(ctx context.Context, userID string, plan string) error {
	u, _ := g.users.Get(ctx, userID)       // loading aggregates
	if u.HasActiveSubscription() {          // business rule
		return nil
	}
	amount := g.pricing.For(plan, u.Tier)   // orchestration
	...
}
```

The adapter should be dumb: take a DTO, call the provider, translate back.

```go
// adapter
func (g *Gateway) Charge(ctx context.Context, req billingsvc.ChargeRequest) (billingsvc.ChargeResult, error)

// service
func (s *Service) Upgrade(ctx context.Context, userID userdom.ID, plan billingdom.Plan) error {
	u, err := s.users.Get(ctx, userID)
	if err != nil {
		return err
	}
	if u.HasActiveSubscription() {
		return nil
	}
	_, err = s.payments.Charge(ctx, ChargeRequest{Customer: u.BillingID, Amount: plan.Price()})
	return err
}
```

### Trap 3: interfaces before there's a second implementation

```go
// internal/clock/clock.go
type Clock interface { Now() time.Time }
type RealClock struct{}
```

A whole package for one implementation. A `func() time.Time` field does the same job. Go's guidance is to not define interfaces "before they are used": add one when there's a real second implementation or a test fake that needs it. Standard library calls (`strings`, `encoding/json`, `filepath`) never need wrapping.

### Trap 4: the interface next to the implementation

```go
package stripegw

type PaymentGateway interface { Charge(...) }    // declared by the producer
func New(...) PaymentGateway { return &gateway{} } // returns the interface
```

This is the Java habit. It forces `billingsvc` to import `gateway/stripegw` for the interface (an outward dependency), and it hides the concrete type. Return `*stripegw.Client`; let `billingsvc` declare the slice of behaviour it needs in its `ports.go`.

### Trap 5: mock frameworks for everything

Generated mocks with `EXPECT().Summarize(gomock.Any()).Return(...)` test *how* the service calls its dependencies, line by line, and break on every refactor. A ten-line hand-written fake in `<svc>test` tests *what* happened, and one fake serves every test of that service. Reach for generated mocks only for large third-party interfaces you can't shrink, and even then prefer shrinking them.

### Trap 6: wrapping a wrapper

If you already use a library that abstracts several LLM providers, adding your own `Summarizer` port on top is a second abstraction over the same problem. It still earns rent if you want your own DTOs and a cheap fake for tests. If "for tests" is the only reason and an `httptest.Server` returning canned JSON would do, skip the port.

## How to spot it in your code

```sh
go list -f '{{.ImportPath}}: {{join .Imports " "}}' ./internal/... | grep -E 'stripe|openai|aws|pgx|database/sql'
```

SDK imports should appear only in the `gateway/` package named after that SDK (or in `repo/` for database drivers). A hit in `internal/service/` or `internal/domain/` is a leak.

Then grep for interfaces declared in adapter packages (`grep -rn 'interface {' internal/repo internal/gateway`). Small helper interfaces the adapter itself consumes (like `listrepo.Querier`, covering `*sql.DB` and `*sql.Tx`) are fine. Interfaces meant for services belong in the service's `ports.go`.

## The hidden benefit

**Ports force you to design your own DTOs.** Writing `listsvc.SummaryRequest` makes you ask what the use case actually needs from a summariser. The answer is almost always two or three fields, not the provider's twenty. That smaller surface is what makes swapping providers, faking in tests, and reading the service all cheap.

And because the interface lives with the consumer, it stays exactly as big as the consumer needs. The provider can grow forty methods; your port still has one.

## Self-check

1. In Go, which package declares a port, which file is it in, and why not the adapter's package?
2. Why can the `openaigw` adapter import `listsvc`, but `listsvc` can't import `openaigw`?
3. Your service calls the Stripe SDK directly. Name three things wrong with that.
4. What does `var _ listsvc.UnitOfWork = (*File)(nil)` buy you?
5. When is a `func() time.Time` better than a `Clock` interface?
6. Why prefer a hand-written fake over a generated mock?
7. Why does `FakeSummarizer` live in `listsvctest` rather than in `gateway/openaigw`?

## Things to steal

- **The consumer declares the interface; the adapter just has the methods.**
- **Accept interfaces, return structs.** Constructors return `*openaigw.Summarizer`, never `listsvc.Summarizer`.
- **Two adapter homes:** `repo/<name>repo` for data you own, `gateway/<vendor>gw` for systems you call. Gateway packages are the vendor name plus `gw` (`openaigw`, `stripegw`, `epicgw`), so they never collide with the vendor's own SDK package.
- **Your DTOs at the seam; provider types unexported in the adapter.**
- **Translate errors at the seam,** wrap the rest with `%w`.
- **Hand-written fakes beside the port,** in `<svc>test`, imported only from tests.
- **The rent test:** a real second implementation or a fake you use today. Otherwise, a function value or nothing.
