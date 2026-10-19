package listdom_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/vsheshjain/code-taste-golang/example/internal/domain/listdom"
)

func TestSlugify(t *testing.T) {
	tests := []struct {
		name, want string
	}{
		{"My Stuff", "my-stuff"},
		{"  Go & Rust!  ", "go-rust"},
		{"already-a-slug", "already-a-slug"},
		{"!!!", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := listdom.Slugify(tt.name); got != tt.want {
				t.Errorf("Slugify(%q) = %q, want %q", tt.name, got, tt.want)
			}
		})
	}
}

func TestNewRejectsEmptyName(t *testing.T) {
	if _, err := listdom.New("l1", "u1", "   "); !errors.Is(err, listdom.ErrEmptyName) {
		t.Fatalf("err = %v, want ErrEmptyName", err)
	}
}

func TestAddItem(t *testing.T) {
	l, _ := listdom.New("l1", "u1", "Reading")

	if err := l.AddItem(listdom.Item{URL: "https://go.dev"}); err != nil {
		t.Fatalf("first add: %v", err)
	}
	if err := l.AddItem(listdom.Item{URL: "https://go.dev"}); !errors.Is(err, listdom.ErrDuplicateItem) {
		t.Fatalf("duplicate add: err = %v, want ErrDuplicateItem", err)
	}
}

func TestAddItemFull(t *testing.T) {
	l, _ := listdom.New("l1", "u1", "Reading")
	for i := range listdom.MaxItems {
		if err := l.AddItem(listdom.Item{URL: fmt.Sprint(i)}); err != nil {
			t.Fatalf("add %d: %v", i, err)
		}
	}
	if err := l.AddItem(listdom.Item{URL: "one-too-many"}); !errors.Is(err, listdom.ErrFull) {
		t.Fatalf("err = %v, want ErrFull", err)
	}
}

func BenchmarkSlugify(b *testing.B) {
	for b.Loop() {
		listdom.Slugify("A Fairly Long List Name With Some Punctuation!")
	}
}
