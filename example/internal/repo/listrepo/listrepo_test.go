package listrepo_test

import (
	"path/filepath"
	"testing"

	"github.com/vsheshjain/code-taste-golang/example/internal/repo/listrepo"
	"github.com/vsheshjain/code-taste-golang/example/internal/service/listsvc"
	"github.com/vsheshjain/code-taste-golang/example/internal/service/listsvc/listsvctest"
)

// The same contract the fake passes; if the two ever disagree, this fails.
func TestFile(t *testing.T) {
	listsvctest.TestUnitOfWork(t, func(t *testing.T) listsvc.UnitOfWork {
		return listrepo.New(filepath.Join(t.TempDir(), "lists.json"))
	})
}
