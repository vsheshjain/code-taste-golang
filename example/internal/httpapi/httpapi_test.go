package httpapi_test

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vsheshjain/code-taste-golang/example/internal/domain/listdom"
	"github.com/vsheshjain/code-taste-golang/example/internal/httpapi"
	"github.com/vsheshjain/code-taste-golang/example/internal/service/listsvc"
	"github.com/vsheshjain/code-taste-golang/example/internal/service/listsvc/listsvctest"
)

func TestListLifecycle(t *testing.T) {
	svc := listsvc.New(&listsvctest.Store{}, func() listdom.ID { return "l1" })
	srv := httptest.NewServer(httpapi.NewHandler(svc, slog.New(slog.DiscardHandler)))
	defer srv.Close()

	steps := []struct {
		method, path, body string
		wantStatus         int
	}{
		{"POST", "/lists", `{"owner":"u1","name":"My Stuff"}`, http.StatusCreated},
		{"POST", "/lists/l1/items", `{"url":"https://go.dev"}`, http.StatusNoContent},
		{"POST", "/lists/l1/items", `{"url":"https://go.dev"}`, http.StatusConflict},
		{"POST", "/lists/missing/items", `{"url":"x"}`, http.StatusNotFound},
		{"POST", "/lists", `{"owner":"u1","name":" "}`, http.StatusUnprocessableEntity},
		{"POST", "/lists", `not json`, http.StatusBadRequest},
	}
	for _, s := range steps {
		req, _ := http.NewRequest(s.method, srv.URL+s.path, strings.NewReader(s.body))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != s.wantStatus {
			t.Errorf("%s %s = %d, want %d", s.method, s.path, resp.StatusCode, s.wantStatus)
		}
	}

	resp, err := http.Get(srv.URL + "/lists/l1")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if want := `"slug":"my-stuff"`; !strings.Contains(string(body), want) {
		t.Errorf("body %s missing %s", body, want)
	}
}
