package gittools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/piedshag/git-review/internal/gitrepo"
	"github.com/piedshag/git-review/internal/toolset"
)

func TestToolsExposeAndCallGitSnapshot(t *testing.T) {
	tools := makeTools(t)
	definitions := tools.Definitions()
	if len(definitions) != 5 {
		t.Fatalf("definitions = %d, want 5", len(definitions))
	}
	wantNames := []string{"stat", "diff", "glob", "grep", "read"}
	for i, want := range wantNames {
		if definitions[i].Name != want {
			t.Fatalf("definition %d = %q, want %q", i, definitions[i].Name, want)
		}
	}

	result, err := tools.Call(context.Background(), "read", json.RawMessage(`{"path":"file.txt"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Text, "target contents") {
		t.Fatalf("read result = %q", result.Text)
	}
	result, err = tools.Call(context.Background(), "read", json.RawMessage(`{"path":"file.txt","ref":"base"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Text, "base contents") {
		t.Fatalf("base read result = %q", result.Text)
	}
}

func TestToolsRejectInvalidAndUnknownArguments(t *testing.T) {
	tools := makeTools(t)
	for _, test := range []struct {
		name string
		raw  string
	}{
		{name: "stat", raw: `{"extra":true}`},
		{name: "read", raw: `{"path":"../secret"}`},
		{name: "grep", raw: `{"pattern":"["}`},
	} {
		if _, err := tools.Call(context.Background(), test.name, json.RawMessage(test.raw)); err == nil {
			t.Errorf("%s accepted %s", test.name, test.raw)
		}
	}
}

func makeTools(t *testing.T) *toolset.Registry {
	t.Helper()
	tools, err := New(initRepository(t))
	if err != nil {
		t.Fatal(err)
	}
	return tools
}

func initRepository(t *testing.T) *gitrepo.Snapshot {
	t.Helper()
	dir := t.TempDir()
	repo, err := git.PlainInit(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	worktree, err := repo.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	write := func(contents string) {
		if err := os.WriteFile(filepath.Join(dir, "file.txt"), []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := worktree.Add("file.txt"); err != nil {
			t.Fatal(err)
		}
	}
	commit := func(message string) plumbing.Hash {
		hash, err := worktree.Commit(message, &git.CommitOptions{Author: &object.Signature{Name: "Test", Email: "test@example.com", When: time.Unix(1, 0)}})
		if err != nil {
			t.Fatal(err)
		}
		return hash
	}
	write("base contents\n")
	base := commit("base")
	if err := repo.Storer.SetReference(plumbing.NewHashReference(plumbing.NewBranchReferenceName("main"), base)); err != nil {
		t.Fatal(err)
	}
	if err := worktree.Checkout(&git.CheckoutOptions{Branch: plumbing.NewBranchReferenceName("feature"), Create: true}); err != nil {
		t.Fatal(err)
	}
	write("target contents\n")
	commit("feature")
	snapshot, err := gitrepo.Open(dir, "main", "feature")
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}
