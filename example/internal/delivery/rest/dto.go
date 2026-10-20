package rest

import "github.com/vsheshjain/code-taste-golang/example/internal/domain/listdom"

// Wire DTOs: the JSON contract of the REST API. They live in the delivery
// adapter because only it speaks JSON; a grpc or consumer adapter would keep
// its own wire shapes the same way. Domain types carry no tags, and
// the repo has its own row shapes, so each of the three can change alone.

type createListRequest struct {
	Owner string `json:"owner"`
	Name  string `json:"name"`
}

// listResponse is the wire shape. It is deliberately separate from
// listdom.List so the JSON contract can change without touching the domain.
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

func toResponse(l *listdom.List) listResponse {
	items := make([]itemJSON, len(l.Items))
	for i, it := range l.Items {
		items[i] = itemJSON(it)
	}
	return listResponse{ID: string(l.ID), Name: l.Name, Slug: l.Slug, Items: items}
}
