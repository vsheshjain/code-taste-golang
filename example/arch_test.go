package example_test

import (
	"os/exec"
	"strings"
	"testing"
)

// TestDependencyRule fails if an inner package imports an outer one.
// The rule is the architecture; this test is how it stays true.
func TestDependencyRule(t *testing.T) {
	const mod = "github.com/vsheshjain/code-taste-golang/example/internal/"
	forbidden := map[string][]string{
		"list":   {"app", "memory", "httpapi"},
		"app":    {"memory", "httpapi"},
		"memory": {"httpapi"},
	}
	for pkg, banned := range forbidden {
		out, err := exec.Command("go", "list", "-deps", "./internal/"+pkg).Output()
		if err != nil {
			t.Fatalf("go list %s: %v", pkg, err)
		}
		deps := strings.Fields(string(out))
		for _, b := range banned {
			for _, d := range deps {
				if d == mod+b {
					t.Errorf("internal/%s imports internal/%s\n"+
						"  WHY: dependencies must point inward (list ← app ← adapters ← httpapi ← cmd)\n"+
						"  FIX: move the shared type inward (internal/app or internal/list), or wire it in cmd/api",
						pkg, b)
				}
			}
		}
	}
}
