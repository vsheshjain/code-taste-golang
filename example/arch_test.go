package example_test

import (
	"os/exec"
	"strings"
	"testing"
)

const internal = "github.com/vsheshjain/code-taste-golang/example/internal/"

// layer names the layer a package belongs to, from its import path.
func layer(pkg string) string {
	rel, ok := strings.CutPrefix(pkg, internal)
	if !ok {
		if strings.Contains(pkg, "/cmd/") {
			return "cmd"
		}
		return ""
	}
	top, rest, _ := strings.Cut(rel, "/")
	if top == "service" && strings.HasSuffix(rest, "svctest") {
		return "svctest"
	}
	return top
}

// allowed lists which internal layers each layer may import.
// Test files are exempt: go list .Imports excludes them, which is what lets
// repo tests run the svctest contract suites.
var allowed = map[string][]string{
	"domain":   {"domain"},
	"service":  {"domain"},
	"svctest":  {"domain", "service"},
	"repo":     {"domain", "service"},
	"gateway":  {"domain", "service"},
	"delivery": {"domain", "service"},
	"cmd":      {"domain", "service", "repo", "gateway", "delivery"},
}

// TestDependencyRule fails if any non-test package imports a layer it
// shouldn't. The rule is the architecture; this test is how it stays true.
func TestDependencyRule(t *testing.T) {
	out, err := exec.Command("go", "list", "-f", `{{.ImportPath}} {{join .Imports " "}}`, "./...").Output()
	if err != nil {
		t.Fatalf("go list: %v", err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		fields := strings.Fields(line)
		pkg, from := fields[0], layer(fields[0])
		for _, imp := range fields[1:] {
			to := layer(imp)
			if to == "" || from == "" {
				continue
			}
			if from == "service" && to == "service" {
				t.Errorf("%s imports %s\n"+
					"  WHY: services never call each other; each service declares what it needs in its own ports.go\n"+
					"  FIX: declare a small interface in this service's ports.go and wire the implementation in cmd/",
					pkg, imp)
				continue
			}
			if from == "repo" && to == "repo" && strings.HasSuffix(pkg, "/pgtx") {
				continue // pgtx composes the per-domain repos onto one transaction
			}
			if !contains(allowed[from], to) {
				t.Errorf("%s (%s) imports %s (%s)\n"+
					"  WHY: dependencies point inward: domain ← service ← repo/gateway/delivery ← cmd; svctest fakes are for _test.go files only\n"+
					"  FIX: move the shared type inward (service ports.go or domain), or do the wiring in cmd/",
					pkg, from, imp, to)
			}
		}
	}
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}
