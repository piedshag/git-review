package pipeline

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/piedshag/git-review/internal/gitrepo"
	"github.com/piedshag/git-review/internal/review"
	"github.com/piedshag/git-review/internal/reviewapp"
)

func TestRunnerExecutesIndependentAgentsConcurrentlyThenJudge(t *testing.T) {
	snapshot := testSnapshot(t)
	started := make(chan string, 2)
	release := make(chan struct{})
	var mu sync.Mutex
	var judgeInputs []review.NamedReview
	execute := func(_ context.Context, snapshot *gitrepo.Snapshot, job reviewapp.Config) (review.Review, error) {
		if len(job.Inputs) > 0 {
			mu.Lock()
			judgeInputs = append([]review.NamedReview(nil), job.Inputs...)
			mu.Unlock()
			return completeReview("judged"), nil
		}
		if output := snapshot.Call("stat", `{}`); strings.HasPrefix(output, "error:") {
			return review.Review{}, fmt.Errorf("inspect shared snapshot: %s", output)
		}
		started <- job.Model
		<-release
		return completeReview(job.Model), nil
	}
	config := Config{
		Output: "final",
		Agents: []Agent{
			{ID: "security", Timeout: time.Second, Job: reviewapp.Config{Model: "security-model"}},
			{ID: "correctness", Timeout: time.Second, Job: reviewapp.Config{Model: "correctness-model"}},
			{ID: "final", Inputs: []string{"security", "correctness"}, Timeout: time.Second, Job: reviewapp.Config{Model: "judge-model"}},
		},
	}

	type outcome struct {
		result RunResult
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		result, err := (Runner{Snapshot: snapshot, Config: config, Execute: execute}).Run(context.Background())
		done <- outcome{result: result, err: err}
	}()
	models := map[string]bool{}
	for range 2 {
		select {
		case model := <-started:
			models[model] = true
		case <-time.After(time.Second):
			t.Fatal("independent agents did not start concurrently")
		}
	}
	close(release)
	finished := <-done
	if finished.err != nil {
		t.Fatal(finished.err)
	}
	if !models["security-model"] || !models["correctness-model"] {
		t.Fatalf("unexpected concurrently started models: %v", models)
	}
	if selected, ok := finished.result.Selected(); !ok || selected.Summary != "judged review summary" {
		t.Fatalf("unexpected selected review: %+v, ok=%t", selected, ok)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(judgeInputs) != 2 || judgeInputs[0].ID != "security" || judgeInputs[1].ID != "correctness" {
		t.Fatalf("unexpected judge inputs: %+v", judgeInputs)
	}
}

func TestRunnerBlocksDependentAgentAfterFailure(t *testing.T) {
	snapshot := testSnapshot(t)
	judgeCalled := false
	execute := func(_ context.Context, _ *gitrepo.Snapshot, job reviewapp.Config) (review.Review, error) {
		if job.Model == "broken" {
			return review.Review{}, errors.New("provider unavailable")
		}
		if job.Model == "judge" {
			judgeCalled = true
		}
		return completeReview(job.Model), nil
	}
	result, err := (Runner{
		Snapshot: snapshot,
		Config: Config{Output: "final", Agents: []Agent{
			{ID: "broken", Timeout: time.Second, Job: reviewapp.Config{Model: "broken"}},
			{ID: "healthy", Timeout: time.Second, Job: reviewapp.Config{Model: "healthy"}},
			{ID: "final", Inputs: []string{"broken", "healthy"}, Timeout: time.Second, Job: reviewapp.Config{Model: "judge"}},
		}},
		Execute: execute,
	}).Run(context.Background())
	if err == nil || judgeCalled {
		t.Fatalf("err=%v judgeCalled=%t", err, judgeCalled)
	}
	if result.Nodes[1].Review == nil || result.Nodes[0].Error == "" || result.Nodes[2].Error == "" {
		t.Fatalf("successful and failed node results were not preserved: %+v", result.Nodes)
	}
}

func testSnapshot(t *testing.T) *gitrepo.Snapshot {
	t.Helper()
	snapshot, err := gitrepo.Open("../..", "HEAD", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func completeReview(name string) review.Review {
	return review.Review{
		Status:     review.ReviewComplete,
		Summary:    name + " review summary",
		Strengths:  "The implementation has clear strengths.",
		Weaknesses: "No material weaknesses were identified.",
		Findings:   []review.Finding{},
	}
}
