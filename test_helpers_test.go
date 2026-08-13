package main

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
)

type completionFunc func(context.Context, []message, []Tool) (message, tokenUsage, error)

func (fn completionFunc) Complete(ctx context.Context, messages []message, tools []Tool) (message, tokenUsage, error) {
	return fn(ctx, messages, tools)
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func makeSnapshot(t *testing.T) *Snapshot {
	t.Helper()
	dir := t.TempDir()
	repo, err := git.PlainInit(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	worktree, _ := repo.Worktree()
	if err := os.WriteFile(filepath.Join(dir, "file.go"), []byte("package example\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := worktree.Add("."); err != nil {
		t.Fatal(err)
	}
	hash, err := worktree.Commit("initial", &git.CommitOptions{Author: &object.Signature{Name: "Test", Email: "test@example.com", When: time.Unix(1, 0)}})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"main", "feature"} {
		if err := repo.Storer.SetReference(plumbing.NewHashReference(plumbing.NewBranchReferenceName(name), hash)); err != nil {
			t.Fatal(err)
		}
	}
	snapshot, err := Open(dir, "main", "feature")
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}
