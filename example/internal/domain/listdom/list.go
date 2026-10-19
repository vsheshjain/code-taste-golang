// Package listdom is the list domain: a named list of items owned by one user,
// and the rules about it. It imports nothing outside the standard library.
package listdom

import (
	"errors"
	"regexp"
	"slices"
	"strings"
)

// MaxItems is the business limit on items per list.
const MaxItems = 100

// Domain errors. Callers match them with errors.Is; adapters translate
// their own failures into these at the seam.
var (
	ErrNotFound      = errors.New("list not found")
	ErrFull          = errors.New("list is full")
	ErrDuplicateItem = errors.New("item already in list")
	ErrEmptyName     = errors.New("list name is empty")
)

// ID identifies a List.
type ID string

// Item is a value object: two items with the same URL are the same item.
type Item struct {
	URL   string
	Title string
}

// List is the aggregate root. Items only exist inside a List.
type List struct {
	ID    ID
	Owner string
	Name  string
	Slug  string
	Items []Item
}

// New builds a valid List or says why it can't.
func New(id ID, owner, name string) (*List, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, ErrEmptyName
	}
	return &List{ID: id, Owner: owner, Name: name, Slug: Slugify(name)}, nil
}

var nonSlug = regexp.MustCompile(`[^a-z0-9]+`)

// Slugify turns a list name into its URL slug.
func Slugify(name string) string {
	return strings.Trim(nonSlug.ReplaceAllString(strings.ToLower(name), "-"), "-")
}

// AddItem enforces the list's rules; the service never checks them itself.
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
