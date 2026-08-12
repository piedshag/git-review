package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
)

func TestSnapshotToolsUseCommitsAndMergeBase(t *testing.T) {
	dir := t.TempDir()
	repo, err := git.PlainInit(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	worktree, err := repo.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, dir, "root.go", "package root\n\nfunc Value() int { return 1 }\n")
	writeFile(t, dir, "docs/old.txt", "old\n")
	if _, err := worktree.Add("."); err != nil {
		t.Fatal(err)
	}
	baseHash := commit(t, worktree, "base")
	if err := repo.Storer.SetReference(plumbing.NewHashReference(plumbing.NewBranchReferenceName("main"), baseHash)); err != nil {
		t.Fatal(err)
	}
	if err := worktree.Checkout(&git.CheckoutOptions{Branch: plumbing.NewBranchReferenceName("feature"), Create: true}); err != nil {
		t.Fatal(err)
	}
	writeFile(t, dir, "root.go", "package root\n\nfunc Value() int { return 2 }\n")
	writeFile(t, dir, "pkg/new.go", "package pkg\n\nfunc New() {}\n")
	if _, err := worktree.Add("."); err != nil {
		t.Fatal(err)
	}
	commit(t, worktree, "feature")

	// Move main forward after the feature diverged. The main-only file must not
	// appear in feature stats because Snapshot compares from the merge-base.
	if err := worktree.Checkout(&git.CheckoutOptions{Branch: plumbing.NewBranchReferenceName("main")}); err != nil {
		t.Fatal(err)
	}
	writeFile(t, dir, "main-only.txt", "not part of feature\n")
	if _, err := worktree.Add("."); err != nil {
		t.Fatal(err)
	}
	commit(t, worktree, "main update")

	snapshot, err := Open(dir, "main", "feature")
	if err != nil {
		t.Fatal(err)
	}
	stats := snapshot.Call("stat", `{}`)
	if !strings.Contains(stats, "root.go") || !strings.Contains(stats, "pkg/new.go") {
		t.Fatalf("missing changed files in stats:\n%s", stats)
	}
	if strings.Contains(stats, "main-only.txt") {
		t.Fatalf("stats included a base-only change:\n%s", stats)
	}

	paths := snapshot.Call("glob", `{"pattern":"**/*.go"}`)
	if !strings.Contains(paths, "root.go") || !strings.Contains(paths, "pkg/new.go") {
		t.Fatalf("recursive glob did not match root and nested files:\n%s", paths)
	}
	matches := snapshot.Call("grep", `{"pattern":"return 2","glob":"*.go"}`)
	if !strings.Contains(matches, "root.go:3:func Value() int { return 2 }") {
		t.Fatalf("unexpected grep output: %s", matches)
	}
	read := snapshot.Call("read", `{"path":"root.go","start":3,"end":3}`)
	if !strings.Contains(read, "3\tfunc Value() int { return 2 }") {
		t.Fatalf("unexpected read output: %s", read)
	}
	baseRead := snapshot.Call("read", `{"path":"root.go","ref":"base","start":3,"end":3}`)
	if !strings.Contains(baseRead, "return 1") {
		t.Fatalf("read did not select base snapshot: %s", baseRead)
	}
}

func TestReadRejectsPathsOutsideRepository(t *testing.T) {
	snapshot := testSnapshot(t)
	result := snapshot.Call("read", `{"path":"../secret"}`)
	if !strings.Contains(result, "repository-relative") {
		t.Fatalf("expected path rejection, got %q", result)
	}
}

func testSnapshot(t *testing.T) *Snapshot {
	t.Helper()
	dir := t.TempDir()
	repo, err := git.PlainInit(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	worktree, _ := repo.Worktree()
	writeFile(t, dir, "file.txt", "content\n")
	if _, err := worktree.Add("."); err != nil {
		t.Fatal(err)
	}
	hash := commit(t, worktree, "initial")
	for _, branch := range []string{"main", "feature"} {
		if err := repo.Storer.SetReference(plumbing.NewHashReference(plumbing.NewBranchReferenceName(branch), hash)); err != nil {
			t.Fatal(err)
		}
	}
	snapshot, err := Open(dir, "main", "feature")
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func writeFile(t *testing.T, root, name, contents string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}

func commit(t *testing.T, worktree *git.Worktree, message string) plumbing.Hash {
	t.Helper()
	hash, err := worktree.Commit(message, &git.CommitOptions{Author: &object.Signature{Name: "Test", Email: "test@example.com", When: time.Unix(1, 0)}})
	if err != nil {
		t.Fatal(err)
	}
	return hash
}
