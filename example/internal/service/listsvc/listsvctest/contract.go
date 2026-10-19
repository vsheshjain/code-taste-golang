package listsvctest

import (
	"context"
	"errors"
	"testing"

	"github.com/vsheshjain/code-taste-golang/example/internal/domain/listdom"
	"github.com/vsheshjain/code-taste-golang/example/internal/service/listsvc"
)

// TestUnitOfWork is the contract every listsvc.UnitOfWork must honour.
// The fake runs it in its own test, and so does every real adapter in
// repo/, so the fake can never drift from production behaviour.
// newUoW must return an empty store each call.
func TestUnitOfWork(t *testing.T, newUoW func(t *testing.T) listsvc.UnitOfWork) {
	ctx := context.Background()

	t.Run("missing list is ErrNotFound", func(t *testing.T) {
		err := newUoW(t).Do(ctx, func(r listsvc.Repos) error {
			_, err := r.Lists.Get(ctx, "nope")
			return err
		})
		if !errors.Is(err, listdom.ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	})

	t.Run("committed save is readable", func(t *testing.T) {
		uow := newUoW(t)
		want := &listdom.List{ID: "l1", Owner: "u1", Name: "A", Slug: "a",
			Items: []listdom.Item{{URL: "https://go.dev", Title: "Go"}}}
		if err := uow.Do(ctx, func(r listsvc.Repos) error { return r.Lists.Save(ctx, want) }); err != nil {
			t.Fatal(err)
		}
		var got *listdom.List
		if err := uow.Do(ctx, func(r listsvc.Repos) (err error) { got, err = r.Lists.Get(ctx, "l1"); return err }); err != nil {
			t.Fatal(err)
		}
		if got.Name != want.Name || len(got.Items) != 1 || got.Items[0] != want.Items[0] {
			t.Fatalf("got %+v, want %+v", got, want)
		}
	})

	t.Run("error rolls back", func(t *testing.T) {
		uow := newUoW(t)
		boom := errors.New("boom")
		err := uow.Do(ctx, func(r listsvc.Repos) error {
			if err := r.Lists.Save(ctx, &listdom.List{ID: "l1", Name: "A"}); err != nil {
				return err
			}
			return boom
		})
		if !errors.Is(err, boom) {
			t.Fatalf("err = %v, want boom", err)
		}
		err = uow.Do(ctx, func(r listsvc.Repos) error {
			_, err := r.Lists.Get(ctx, "l1")
			return err
		})
		if !errors.Is(err, listdom.ErrNotFound) {
			t.Fatalf("after rollback: err = %v, want ErrNotFound", err)
		}
	})
}
