package listsvctest_test

import (
	"testing"

	"github.com/vsheshjain/code-taste-golang/example/internal/service/listsvc"
	"github.com/vsheshjain/code-taste-golang/example/internal/service/listsvc/listsvctest"
)

func TestStore(t *testing.T) {
	listsvctest.TestUnitOfWork(t, func(*testing.T) listsvc.UnitOfWork { return &listsvctest.Store{} })
}
