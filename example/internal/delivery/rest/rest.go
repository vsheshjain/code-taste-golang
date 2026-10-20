// Package rest is the REST delivery adapter: decode the request, call the
// service, encode the response. No business rules, no storage.
package rest

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/vsheshjain/code-taste-golang/example/internal/domain/listdom"
	"github.com/vsheshjain/code-taste-golang/example/internal/service/listsvc"
)

// NewHandler returns the API's routes.
func NewHandler(svc *listsvc.Service, log *slog.Logger) http.Handler {
	h := &handler{svc: svc, log: log}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /lists", h.createList)
	mux.HandleFunc("GET /lists/{id}", h.getList)
	mux.HandleFunc("POST /lists/{id}/items", h.addItem)
	return mux
}

type handler struct {
	svc *listsvc.Service
	log *slog.Logger
}

func (h *handler) createList(w http.ResponseWriter, r *http.Request) {
	var req createListRequest
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
	l, err := h.svc.GetList(r.Context(), listdom.ID(r.PathValue("id")))
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
	if err := h.svc.AddItem(r.Context(), listdom.ID(r.PathValue("id")), listdom.Item(req)); err != nil {
		h.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// fail maps domain errors to status codes. This is the only place that
// knows both vocabularies.
func (h *handler) fail(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, listdom.ErrNotFound):
		http.Error(w, err.Error(), http.StatusNotFound)
	case errors.Is(err, listdom.ErrFull), errors.Is(err, listdom.ErrDuplicateItem):
		http.Error(w, err.Error(), http.StatusConflict)
	case errors.Is(err, listdom.ErrEmptyName):
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
