// Package listrepo is the storage adapter for lists: it implements
// listsvc.UnitOfWork over a JSON file. A Postgres version would keep the
// same shape, swapping the file for a *sql.Tx.
package listrepo

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sync"

	"github.com/vsheshjain/code-taste-golang/example/internal/domain/listdom"
	"github.com/vsheshjain/code-taste-golang/example/internal/service/listsvc"
)

var _ listsvc.UnitOfWork = (*File)(nil)

// File stores every list in one JSON file.
//
// ponytail: whole-file rewrite under one lock; fine for a demo, a real
// database is the upgrade path once there's concurrent load.
type File struct {
	path string
	mu   sync.Mutex
}

// New returns a File backed by path. The file is created on first commit.
func New(path string) *File {
	return &File{path: path}
}

// Do loads the file, runs fn against it, and writes it back only if fn
// returns nil. The write goes to a temp file renamed into place, so a crash
// never leaves a half-written file.
func (f *File) Do(ctx context.Context, fn func(listsvc.Repos) error) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	rows, err := f.load()
	if err != nil {
		return err
	}
	if err := fn(listsvc.Repos{Lists: &lists{rows: rows}}); err != nil {
		return err // rollback: nothing written
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return f.save(rows)
}

// listRow and itemRow are the persistence shape. They are unexported: the
// JSON layout on disk is this package's business, not the domain's.
type listRow struct {
	ID    string    `json:"id"`
	Owner string    `json:"owner"`
	Name  string    `json:"name"`
	Slug  string    `json:"slug"`
	Items []itemRow `json:"items"`
}

type itemRow struct {
	URL   string `json:"url"`
	Title string `json:"title,omitempty"`
}

func (f *File) load() (map[string]listRow, error) {
	rows := map[string]listRow{}
	b, err := os.ReadFile(f.path)
	if errors.Is(err, fs.ErrNotExist) {
		return rows, nil
	}
	if err != nil {
		return nil, err
	}
	return rows, json.Unmarshal(b, &rows)
}

func (f *File) save(rows map[string]listRow) error {
	b, err := json.Marshal(rows)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(f.path), ".listrepo-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // no-op after a successful rename
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), f.path)
}

type lists struct {
	rows map[string]listRow
}

func (r *lists) Get(_ context.Context, id listdom.ID) (*listdom.List, error) {
	row, ok := r.rows[string(id)]
	if !ok {
		return nil, listdom.ErrNotFound // translate at the seam
	}
	l := &listdom.List{ID: listdom.ID(row.ID), Owner: row.Owner, Name: row.Name, Slug: row.Slug}
	for _, it := range row.Items {
		l.Items = append(l.Items, listdom.Item(it))
	}
	return l, nil
}

func (r *lists) Save(_ context.Context, l *listdom.List) error {
	row := listRow{ID: string(l.ID), Owner: l.Owner, Name: l.Name, Slug: l.Slug}
	for _, it := range l.Items {
		row.Items = append(row.Items, itemRow(it))
	}
	r.rows[row.ID] = row
	return nil
}
