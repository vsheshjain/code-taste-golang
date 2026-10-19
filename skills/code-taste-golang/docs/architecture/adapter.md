# Adapter (and Port)

← [Back to index](./README.md)

## One-line definition

An adapter is **a swappable seam between your code and an external system**, and a port is **the interface that defines the seam**.

If you understood Repository, you've already used the pattern. Adapter is the generalisation: *not every external system is a database.*

## Port and adapter: what each word means in Go

| Term | What it is in Go | Example |
|---|---|---|
| **Port** | An interface **declared in the package that consumes it** (`internal/app`). Small, often one or two methods. | `app.Lists`, `app.Summarizer`, `app.Payments` |
| **Adapter** | A concrete struct in a package named after the technology it talks to. Satisfies the port implicitly. | `postgres.Lists`, `openai.Summarizer`, `stripe.Gateway` |

Mental model: the **port is the shape of the hole**, the **adapter is the plug**. The service only knows the hole.

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
app.Lists (interface, in internal/app)       ← port
    ↑ satisfied implicitly by
    ├── *postgres.Lists                        ← production
    └── memory.Store's lists                   ← tests and local runs
```

Same shape for an LLM:

```
app.Summarizer
    ├── *openai.Summarizer
    ├── *anthropic.Summarizer
    └── *fakeSummarizer   (in a _test.go file)
```

And for payments:

```
app.Payments
    ├── *stripe.Gateway
    └── *fakePayments     (in a _test.go file)
```

Once you've seen one, you've seen them all.

## What it looks like

Port and DTOs, declared by the consumer:

```go
// internal/app/summarize.go
package app

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

func (s *Service) Summarize(ctx context.Context, id list.ID) (Summary, error) {
	l, err := s.GetList(ctx, id)
	if err != nil {
		return Summary{}, err
	}
	return s.summarizer.Summarize(ctx, SummaryRequest{Text: l.AsText(), MaxWords: 50})
}
```

The real adapter, in a package named for the provider:

```go
// internal/openai/summarizer.go
package openai

type Summarizer struct {
	client *http.Client
	apiKey string
	model  string
}

func NewSummarizer(client *http.Client, apiKey, model string) *Summarizer {
	return &Summarizer{client: client, apiKey: apiKey, model: model}
}

func (s *Summarizer) Summarize(ctx context.Context, req app.SummaryRequest) (app.Summary, error) {
	body, _ := json.Marshal(chatRequest{
		Model:    s.model,
		Messages: []message{{Role: "user", Content: fmt.Sprintf("Summarize in %d words: %s", req.MaxWords, req.Text)}},
	})
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.openai.com/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return app.Summary{}, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+s.apiKey)
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(httpReq)
	if err != nil {
		return app.Summary{}, fmt.Errorf("openai: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusTooManyRequests {
		return app.Summary{}, app.ErrRateLimited // translate to our vocabulary
	}
	if resp.StatusCode != http.StatusOK {
		return app.Summary{}, fmt.Errorf("openai: status %d", resp.StatusCode)
	}

	var out chatResponse // unexported: the provider's shape stays here
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return app.Summary{}, fmt.Errorf("openai: decode: %w", err)
	}
	return app.Summary{Text: out.Choices[0].Message.Content, TokensUsed: out.Usage.TotalTokens}, nil
}
```

The fake, hand-written, in the test file that needs it:

```go
// internal/app/summarize_test.go
type fakeSummarizer struct {
	reply string
	calls []app.SummaryRequest
}

func (f *fakeSummarizer) Summarize(_ context.Context, req app.SummaryRequest) (app.Summary, error) {
	f.calls = append(f.calls, req)
	return app.Summary{Text: f.reply, TokensUsed: 42}, nil
}
```

And the wiring, once, in [cmd/api/main.go](../../../../example/cmd/api/main.go):

```go
svc := app.New(uow, openai.NewSummarizer(http.DefaultClient, cfg.OpenAIKey, "gpt-4o-mini"), newID)
```

Things to notice:

1. **The port defines its own DTOs.** `app.SummaryRequest`, not the provider's request type. The provider's `chatRequest`/`chatResponse` are unexported in `openai`, so they physically can't leak.
2. **The adapter imports `app`** to name the DTOs in its method signature. That's allowed: adapters point inward. `app` never imports `openai`.
3. **Errors are translated at the seam.** A 429 becomes `app.ErrRateLimited`, which the service or handler can match with `errors.Is`. Everything else is wrapped with `%w` and a prefix that names the adapter.
4. **`ctx` flows into the outgoing request** with `NewRequestWithContext`, so a cancelled HTTP request upstream cancels the LLM call.
5. **The fake is a plain struct**, not a mock framework. It records calls in a slice; the test asserts on the slice. No `EXPECT()`, no generated code.

### Compile-time interface checks

Because satisfaction is implicit, a typo in a method signature only surfaces where the adapter is passed to the service. To catch it in the adapter's own package, add one line:

```go
var _ app.UnitOfWork = (*Store)(nil)
```

The example's [memory adapter](../../../../example/internal/memory/memory.go) does exactly this. It costs nothing at runtime.

## Real examples

| External system | Port (in `app`) | Real adapter | Test adapter |
|---|---|---|---|
| Storage | `Lists`, `UnitOfWork` | `postgres.Store` | `memory.Store` |
| LLM | `Summarizer` | `openai.Summarizer` | `fakeSummarizer` |
| Payments | `Payments` | `stripe.Gateway` | `fakePayments` |
| Email | `Mailer` | `ses.Mailer` | `fakeMailer` |
| Auth | `Verifier` | `clerk.Verifier` | `fakeVerifier` |

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

`app` now imports the SDK, and the service unpacks provider JSON. Define your own `Summary` and let the adapter translate.

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
func (g *Gateway) Charge(ctx context.Context, req app.ChargeRequest) (app.ChargeResult, error)

// service
func (s *Service) Upgrade(ctx context.Context, userID user.ID, plan billing.Plan) error {
	u, err := s.users.Get(ctx, userID)
	if err != nil {
		return err
	}
	if u.HasActiveSubscription() {
		return nil
	}
	_, err = s.payments.Charge(ctx, app.ChargeRequest{Customer: u.BillingID, Amount: plan.Price()})
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
package stripe

type PaymentGateway interface { Charge(...) }    // declared by the producer
func New(...) PaymentGateway { return &gateway{} } // returns the interface
```

This is the Java habit. It forces `app` to import `stripe` for the interface (an outward dependency), and it hides the concrete type. Return `*stripe.Gateway`; let `app` declare the slice of behaviour it needs.

### Trap 5: mock frameworks for everything

Generated mocks with `EXPECT().Summarize(gomock.Any()).Return(...)` test *how* the service calls its dependencies, line by line, and break on every refactor. A ten-line hand-written fake tests *what* happened. Reach for generated mocks only for large third-party interfaces you can't shrink, and even then prefer shrinking them.

### Trap 6: wrapping a wrapper

If you already use a library that abstracts several LLM providers, adding your own `Summarizer` port on top is a second abstraction over the same problem. It still earns rent if you want your own DTOs and a cheap fake for tests. If "for tests" is the only reason and an `httptest.Server` returning canned JSON would do, skip the port.

## How to spot it in your code

```sh
go list -f '{{.ImportPath}}: {{join .Imports " "}}' ./internal/... | grep -E 'stripe|openai|aws|pgx|database/sql'
```

SDK imports should appear only in the package named after that SDK. A hit in `internal/app` or the domain is a leak.

Then grep for interfaces declared in adapter packages (`grep -rn 'interface {' internal/postgres internal/stripe ...`). Unexported helper interfaces (like a `querier` covering `*sql.DB` and `*sql.Tx`) are fine. Exported ones meant for consumers belong in the consumer.

## The hidden benefit

**Ports force you to design your own DTOs.** Writing `app.SummaryRequest` makes you ask what the use case actually needs from a summariser. The answer is almost always two or three fields, not the provider's twenty. That smaller surface is what makes swapping providers, faking in tests, and reading the service all cheap.

And because the interface lives with the consumer, it stays exactly as big as the consumer needs. The provider can grow forty methods; your port still has one.

## Self-check

1. In Go, which package declares a port, and why not the adapter's package?
2. Why can the `openai` adapter import `app`, but `app` can't import `openai`?
3. Your service calls the Stripe SDK directly. Name three things wrong with that.
4. What does `var _ app.UnitOfWork = (*Store)(nil)` buy you?
5. When is a `func() time.Time` better than a `Clock` interface?
6. Why prefer a hand-written fake over a generated mock?

## Things to steal

- **The consumer declares the interface; the adapter just has the methods.**
- **Accept interfaces, return structs.** Constructors return `*openai.Summarizer`, never `app.Summarizer`.
- **Packages named for the technology:** `postgres`, `openai`, `stripe`. Not `adapters`, not `infra`.
- **Your DTOs at the seam; provider types unexported in the adapter.**
- **Translate errors at the seam,** wrap the rest with `%w`.
- **Hand-written fakes beside the tests.**
- **The rent test:** a real second implementation or a fake you use today. Otherwise, a function value or nothing.
