package app_test

import (
	"context"
	"errors"
	"testing"

	"github.com/vsheshjain/code-taste-golang/example/internal/app"
	"github.com/vsheshjain/code-taste-golang/example/internal/list"
	"github.com/vsheshjain/code-taste-golang/example/internal/memory"
)

func newService() *app.Service {
	return app.New(&memory.Store{}, func() list.ID { return "l1" })
}

func TestCreateListAssignsSlug(t *testing.T) {
	ctx := context.Background()
	svc := newService()

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

func TestAddItemMissingList(t *testing.T) {
	err := newService().AddItem(context.Background(), "nope", list.Item{URL: "x"})
	if !errors.Is(err, list.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestFailedUseCaseRollsBack(t *testing.T) {
	ctx := context.Background()
	store := &memory.Store{}
	svc := app.New(store, func() list.ID { return "l1" })
	if _, err := svc.CreateList(ctx, "u1", "Reading"); err != nil {
		t.Fatal(err)
	}

	boom := errors.New("boom")
	err := store.Do(ctx, func(r app.Repos) error {
		l, _ := r.Lists.Get(ctx, "l1")
		_ = l.AddItem(list.Item{URL: "https://go.dev"})
		_ = r.Lists.Save(ctx, l)
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want boom", err)
	}

	got, _ := svc.GetList(ctx, "l1")
	if len(got.Items) != 0 {
		t.Fatalf("items = %v, want none after rollback", got.Items)
	}
}
