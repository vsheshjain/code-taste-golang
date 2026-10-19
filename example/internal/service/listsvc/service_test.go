package listsvc_test

import (
	"context"
	"errors"
	"testing"

	"github.com/vsheshjain/code-taste-golang/example/internal/domain/listdom"
	"github.com/vsheshjain/code-taste-golang/example/internal/service/listsvc"
	"github.com/vsheshjain/code-taste-golang/example/internal/service/listsvc/listsvctest"
)

func newService() *listsvc.Service {
	return listsvc.New(&listsvctest.Store{}, func() listdom.ID { return "l1" })
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
	err := newService().AddItem(context.Background(), "nope", listdom.Item{URL: "x"})
	if !errors.Is(err, listdom.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestAddItemRuleViolationSavesNothing(t *testing.T) {
	ctx := context.Background()
	svc := newService()
	if _, err := svc.CreateList(ctx, "u1", "Reading"); err != nil {
		t.Fatal(err)
	}
	if err := svc.AddItem(ctx, "l1", listdom.Item{URL: "https://go.dev"}); err != nil {
		t.Fatal(err)
	}
	if err := svc.AddItem(ctx, "l1", listdom.Item{URL: "https://go.dev"}); !errors.Is(err, listdom.ErrDuplicateItem) {
		t.Fatalf("err = %v, want ErrDuplicateItem", err)
	}
	got, _ := svc.GetList(ctx, "l1")
	if len(got.Items) != 1 {
		t.Fatalf("items = %v, want exactly one", got.Items)
	}
}
