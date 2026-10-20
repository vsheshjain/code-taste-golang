# Domain

← [Back to index](./README.md)

## One-line definition

The domain is **what your app is fundamentally about, in business terms, not the tech.**

If a non-developer asked you "what does your app do?", the words you'd use are domain words.

## What's the domain in real projects?

| Project | Domain | Domain concepts | Example domain rules |
|---|---|---|---|
| A reading-list app ([the example](../../../../example/internal/domain/listdom/list.go)) | Curated lists of links | `List`, `Item`, owner | "a list holds at most 100 items"; "the same URL can't appear twice in one list" |
| A chat app over LLMs | Conversations | `Conversation`, `Message`, `Turn` | "a turn has one user message and one assistant message"; "a conversation belongs to one user" |
| A billing service | Subscriptions and payments | `Subscription`, `Plan`, `Invoice` | "a subscription is active if it's paid and not expired" |
| `net/http` | HTTP | `Request`, `Response`, `Header` | "a redirect chain has a max length"; "header keys are canonicalised" |

Notice: **none of these mention Postgres, `net/http` routing, pgx or the OpenAI SDK.** Those are infrastructure. The domain is what would still exist if you rewrote the app in another language.

## What lives in the domain *package* (the actual code)

In this layout every domain gets its own small package under `internal/domain/`, one per bounded context: `internal/domain/listdom`, `internal/domain/patientdom`, `internal/domain/emrdom`. The folder name *is* the package name, as the [package names blog post](https://go.dev/blog/package-names) asks. The `dom` suffix is deliberate: the same concept also has a service (`listsvc`) and a repository (`listrepo`), and distinct suffixes mean no import aliases and you always know which layer an identifier comes from.

What the blog post rules out still applies: no catch-all `internal/models`, `types`, `common` or `util` package that every layer imports. `domain/` is a folder that groups the domain packages; it is never a package itself.

Inside it you find:

- **Types**: plain structs. `listdom.List`, `listdom.Item`. No struct tags (`json:`, `db:`), because tags are a wire or storage concern.
- **Constructors**: `New` functions that return a valid value or an error. Invalid states don't get built.
- **Methods that enforce rules**: `(*List).AddItem` decides whether an item is allowed.
- **Sentinel errors**: `var ErrFull = errors.New("list is full")`. Callers match them with `errors.Is`.

From the example:

```go
package listdom

var (
	ErrNotFound      = errors.New("list not found")
	ErrFull          = errors.New("list is full")
	ErrDuplicateItem = errors.New("item already in list")
	ErrEmptyName     = errors.New("list name is empty")
)

type List struct {
	ID    ID
	Owner string
	Name  string
	Slug  string
	Items []Item
}

func New(id ID, owner, name string) (*List, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, ErrEmptyName
	}
	return &List{ID: id, Owner: owner, Name: name, Slug: Slugify(name)}, nil
}

func (l *List) AddItem(it Item) error {
	if len(l.Items) >= MaxItems {
		return ErrFull
	}
	if slices.ContainsFunc(l.Items, func(x Item) bool { return x.URL == it.URL }) {
		return ErrDuplicateItem
	}
	l.Items = append(l.Items, it)
	return nil
}
```

Read the call site out loud: `listdom.New`, `listdom.ErrFull`, `l.AddItem(it)`. The package name is part of every identifier, so the identifiers stay short. That's why it's `listdom.New`, not `listdom.NewList`, and `listdom.ErrFull`, not `listdom.ListFullError`. [Effective Go](https://go.dev/doc/effective_go#package-names) calls this out: the importer sees `bufio.Reader`, so the type isn't `BufReader`.

## What does NOT belong in the domain

- `database/sql`, pgx, or any driver import
- `net/http`, request/response types, status codes
- External SDK calls (`openai.Client`, `stripe.Charge`)
- `os.Getenv`, file paths, config
- `context.Context` on pure rule methods. `AddItem` does no I/O, so it takes no context. Context belongs on things that can block or be cancelled.
- `log/slog`. A rule returns an error; the edge decides whether to log it.

If the domain package's import list has anything outside the standard library, it has been polluted. That check is a one-liner: `go list -deps ./internal/domain/listdom`. The example's [arch_test.go](../../../../example/arch_test.go) enforces it in `go test`: a package under `domain/` may import only the standard library and other domain packages (in one direction, e.g. `emrdom` → `patientdom`).

## The trap: anemic vs rich domain models

Two ways to organise business logic, and the most important distinction in this topic.

**Anemic domain model** (what most Go code in the wild looks like):

```go
type List struct { // just data, often with db and json tags
	ID    string `db:"id" json:"id"`
	Items []Item `json:"items"`
}

func (s *ListService) AddItem(ctx context.Context, id string, it Item) error {
	l, _ := s.repo.Get(ctx, id)
	if len(l.Items) >= 100 { // rule lives in the service
		return errors.New("full")
	}
	...
}
```

**Rich domain model** (what this playbook recommends when the domain has real rules):

```go
func (l *List) AddItem(it Item) error { ... } // rule lives on the type

func (s *Service) AddItem(ctx context.Context, id listdom.ID, it listdom.Item) error {
	return s.uow.Do(ctx, func(r Repos) error {
		l, err := r.Lists.Get(ctx, id)
		if err != nil {
			return err
		}
		if err := l.AddItem(it); err != nil { // service delegates
			return err
		}
		return r.Lists.Save(ctx, l)
	})
}
```

Go has no classes, but it has methods on types, and that's all a rich model needs. The anemic version isn't wrong for pure CRUD. It's a smell once the `if`s in your service are about the business rather than about I/O.

### A Go-specific trap: getters and setters

Don't write `func (l *List) GetName() string` and `SetName`. [Effective Go](https://go.dev/doc/effective_go#Getters) says exported fields are fine, and if you need a getter it's `Name()`, not `GetName()`. Add a method only when it enforces a rule. If changing a field must be validated, unexport the field and expose the rule (`Rename(name string) error`), not a setter.

### Another: the shared `models` package

A single `internal/models` package that holds every struct, imported by every layer, is the Go version of "the ORM model is the domain." It ends up with `db` tags, `json` tags and validation tags on the same struct, and every change ripples everywhere. Keep three shapes (see [project-structure.md](./project-structure.md)): the domain type in `domain/<name>dom` with no tags, the storage row unexported inside `repo/<name>repo`, and the wire DTO in `delivery/rest/dto.go`. Each can then change without touching the other two.

## How to spot it in your code

Open a service method and ask: "is anything here *only* about business rules, with no DB or HTTP?" If yes, it probably belongs on the domain type.

Typical candidates:

- Slug generation, name normalisation, price calculation. Pure functions of domain data: move them to the domain package (`listdom.Slugify`).
- Validation in the HTTP handler (`if req.Name == ""`). The handler may reject malformed JSON, but "a list needs a name" is a domain rule: put it in `listdom.New`.
- `errors.New("...")` created inline in a service. If callers need to react to it, it's a domain sentinel and belongs next to the type.

## Self-check

1. What's the domain of the example app, in one sentence without tech words?
2. Why does `(*listdom.List).AddItem` take no `context.Context`, while `listsvc.Store.Get` does?
3. Why are there no `json:` tags on `listdom.List`?
4. What's the difference between anemic and rich domain models, and when is anemic fine?

If you can answer all four without looking, move on to Repository.

## Things to steal

- **One package per domain, under `internal/domain/`.** `listdom`, `patientdom`, `billingdom`. Never a shared `models` package.
- **Constructors validate.** If `New` returned it, it's valid.
- **Sentinel errors are part of the domain's vocabulary.** `listdom.ErrFull` is a business fact, so it lives in `listdom`.
- **The import list is the purity test.** Standard library only.
- **Methods for rules, fields for data.** No getters and setters for their own sake.
- **Choose anemic knowingly.** For CRUD it's fine. Just don't drift into it by accident.
