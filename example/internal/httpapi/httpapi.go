// Package httpapi is the HTTP entrypoint: decode the request, call the
// service, encode the response. No business rules, no storage.
package httpapi

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/vsheshjain/code-taste-golang/example/internal/app"
	"github.com/vsheshjain/code-taste-golang/example/internal/list"
)

// listResponse is the wire shape. It is deliberately separate from
// list.List so the JSON contract can change without touching the domain.
type listResponse struct {
	ID    string     `json:"id"`
	Name  string     `json:"name"`
	Slug  string     `json:"slug"`
	Items []itemJSON `json:"items"`
}

type itemJSON struct {
	URL   string `json:"url"`
	Title string `json:"title,omitempty"`
}

func toResponse(l *list.List) listResponse {
	items := make([]itemJSON, len(l.Items))
	for i, it := range l.Items {
		items[i] = itemJSON(it)
	}
	return listResponse{ID: string(l.ID), Name: l.Name, Slug: l.Slug, Items: items}
}

// NewHandler returns the API's routes.
func NewHandler(svc *app.Service, log *slog.Logger) http.Handler {
	h := &handler{svc: svc, log: log}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /lists", h.createList)
	mux.HandleFunc("GET /lists/{id}", h.getList)
	mux.HandleFunc("POST /lists/{id}/items", h.addItem)
	return mux
}

type handler struct {
	svc *app.Service
	log *slog.Logger
}

func (h *handler) createList(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Owner string `json:"owner"`
		Name  string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	l, err := h.svc.CreateList(r.Context(), req.Owner, req.Name)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, toResponse(l))
}

func (h *handler) getList(w http.ResponseWriter, r *http.Request) {
	l, err := h.svc.GetList(r.Context(), list.ID(r.PathValue("id")))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toResponse(l))
}

func (h *handler) addItem(w http.ResponseWriter, r *http.Request) {
	var req itemJSON
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if err := h.svc.AddItem(r.Context(), list.ID(r.PathValue("id")), list.Item(req)); err != nil {
		h.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// fail maps domain errors to status codes. This is the only place that
// knows both vocabularies.
func (h *handler) fail(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, list.ErrNotFound):
		http.Error(w, err.Error(), http.StatusNotFound)
	case errors.Is(err, list.ErrFull), errors.Is(err, list.ErrDuplicateItem):
		http.Error(w, err.Error(), http.StatusConflict)
	case errors.Is(err, list.ErrEmptyName):
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
	default:
		h.log.ErrorContext(r.Context(), "request failed", "method", r.Method, "path", r.URL.Path, "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
