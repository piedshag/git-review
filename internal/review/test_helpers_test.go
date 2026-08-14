package review

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/piedshag/git-review/internal/agent"
	"github.com/piedshag/git-review/internal/gitrepo"
	"github.com/piedshag/git-review/internal/gittools"
	"github.com/piedshag/git-review/internal/toolset"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
)

const (
	testChangeSummary = "The branch updates the review pipeline and its output behavior."
	testStrengths     = "The implementation keeps responsibilities clear and behavior deterministic."
	testWeaknesses    = "No material weaknesses were identified in the inspected changes."
)

func reviewArguments(findings string) string {
	return fmt.Sprintf(`{"summary":%q,"strengths":%q,"weaknesses":%q,"findings":%s}`,
		testChangeSummary, testStrengths, testWeaknesses, findings)
}

type completionFunc func(context.Context, []message, []Tool) (message, tokenUsage, error)

func (fn completionFunc) Complete(ctx context.Context, messages []message, tools []Tool) (message, tokenUsage, error) {
	return fn(ctx, messages, tools)
}

func makeSnapshot(t *testing.T) *gitrepo.Snapshot {
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
	snapshot, err := gitrepo.Open(dir, "main", "feature")
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func newTestReviewer(t *testing.T, config Config, model agent.CompletionClient) (*Reviewer, error) {
	t.Helper()
	snapshot := makeSnapshot(t)
	gitSet, err := gittools.New(snapshot)
	if err != nil {
		return nil, err
	}
	submissionSet, err := SubmissionTools()
	if err != nil {
		return nil, err
	}
	tools, err := toolset.Combine(gitSet, submissionSet)
	if err != nil {
		return nil, err
	}
	return New(config, snapshot.Description(), tools, model)
}
