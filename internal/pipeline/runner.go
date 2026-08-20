package pipeline

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/piedshag/git-review/internal/gitrepo"
	"github.com/piedshag/git-review/internal/report"
	"github.com/piedshag/git-review/internal/review"
	"github.com/piedshag/git-review/internal/reviewapp"
)

type ExecuteFunc func(context.Context, *gitrepo.Snapshot, reviewapp.Config) (review.Review, error)

type Runner struct {
	Snapshot    *gitrepo.Snapshot
	Config      Config
	Coordinator *report.Coordinator
	Execute     ExecuteFunc
}

type RunResult struct {
	SchemaVersion int              `json:"schema_version"`
	Snapshot      gitrepo.Identity `json:"snapshot"`
	Output        string           `json:"output"`
	Nodes         []NodeResult     `json:"nodes"`
	selected      *review.Review
}

type NodeResult struct {
	ID     string         `json:"id"`
	Model  string         `json:"model"`
	Inputs []string       `json:"inputs,omitempty"`
	Review *review.Review `json:"review,omitempty"`
	Error  string         `json:"error,omitempty"`
}

type nodeState struct {
	result NodeResult
	done   chan struct{}
}

func (r Runner) Run(ctx context.Context) (RunResult, error) {
	if r.Snapshot == nil {
		return RunResult{}, errors.New("repository snapshot is required")
	}
	if err := validateGraph(&r.Config); err != nil {
		return RunResult{}, err
	}
	coordinator := r.Coordinator
	if coordinator == nil {
		coordinator = report.NewCoordinator(nil, false, false, false)
	}
	execute := r.Execute
	if execute == nil {
		execute = reviewapp.Run
	}
	states := make(map[string]*nodeState, len(r.Config.Agents))
	for _, configured := range r.Config.Agents {
		states[configured.ID] = &nodeState{
			result: NodeResult{ID: configured.ID, Model: configured.Job.Model, Inputs: append([]string(nil), configured.Inputs...)},
			done:   make(chan struct{}),
		}
	}

	var wait sync.WaitGroup
	for _, configured := range r.Config.Agents {
		configured := configured
		state := states[configured.ID]
		wait.Add(1)
		go func() {
			defer wait.Done()
			defer close(state.done)
			if len(configured.Inputs) > 0 {
				coordinator.Lifecycle(configured.ID, "waiting for %v", configured.Inputs)
			}
			inputs, blockedBy, waitErr := awaitInputs(ctx, configured, states)
			if waitErr != nil {
				state.result.Error = waitErr.Error()
				coordinator.Lifecycle(configured.ID, "cancelled: %v", waitErr)
				return
			}
			if blockedBy != "" {
				state.result.Error = "blocked by failed input " + blockedBy
				coordinator.Lifecycle(configured.ID, "%s", state.result.Error)
				return
			}
			configured.Job.Inputs = inputs
			configured.Job.Reporter = coordinator.Agent(configured.ID)
			if len(inputs) == 0 {
				coordinator.Lifecycle(configured.ID, "started with %s", configured.Job.Model)
			} else {
				coordinator.Lifecycle(configured.ID, "started with %s and %d input reviews", configured.Job.Model, len(inputs))
			}
			nodeContext, cancel := context.WithTimeout(ctx, configured.Timeout)
			nodeReview, err := execute(nodeContext, r.Snapshot, configured.Job)
			cancel()
			if err != nil {
				state.result.Error = err.Error()
				coordinator.Lifecycle(configured.ID, "failed: %v", err)
				return
			}
			nodeReview = attributeFindings(configured.ID, configured.Job.Model, len(configured.Inputs) == 0, nodeReview)
			state.result.Review = &nodeReview
			if nodeReview.Status == review.ReviewInconclusive {
				coordinator.Lifecycle(configured.ID, "inconclusive")
			} else {
				coordinator.Lifecycle(configured.ID, "complete with %d findings", len(nodeReview.Findings))
			}
		}()
	}
	wait.Wait()

	result := RunResult{
		SchemaVersion: 2,
		Snapshot:      r.Snapshot.Identity(),
		Output:        r.Config.Output,
		Nodes:         make([]NodeResult, 0, len(r.Config.Agents)),
	}
	for _, configured := range r.Config.Agents {
		node := states[configured.ID].result
		result.Nodes = append(result.Nodes, node)
		if configured.ID == r.Config.Output {
			result.selected = node.Review
		}
	}
	return result, result.Err()
}

func attributeFindings(agentID, model string, independent bool, value review.Review) review.Review {
	value.Findings = append([]review.Finding(nil), value.Findings...)
	for index := range value.Findings {
		finding := &value.Findings[index]
		finding.ID = fmt.Sprintf("%s:%d", agentID, index+1)
		finding.Sources = append([]review.FindingSource(nil), finding.Sources...)
		if independent || len(finding.Sources) == 0 {
			finding.Sources = []review.FindingSource{{FindingID: finding.ID, Agent: agentID, Model: model}}
		}
	}
	return value
}

func awaitInputs(ctx context.Context, configured Agent, states map[string]*nodeState) ([]review.NamedReview, string, error) {
	inputs := make([]review.NamedReview, 0, len(configured.Inputs))
	for _, inputID := range configured.Inputs {
		input := states[inputID]
		select {
		case <-ctx.Done():
			return nil, "", ctx.Err()
		case <-input.done:
		}
		if input.result.Error != "" || input.result.Review == nil {
			return nil, inputID, nil
		}
		inputs = append(inputs, review.NamedReview{ID: inputID, Model: input.result.Model, Review: *input.result.Review})
	}
	return inputs, "", nil
}

func (r RunResult) Selected() (review.Review, bool) {
	if r.selected != nil {
		return *r.selected, true
	}
	for _, node := range r.Nodes {
		if node.ID == r.Output && node.Review != nil {
			return *node.Review, true
		}
	}
	return review.Review{}, false
}

func (r RunResult) Err() error {
	var failures []error
	for _, node := range r.Nodes {
		if node.Error != "" {
			failures = append(failures, fmt.Errorf("%s: %s", node.ID, node.Error))
		}
	}
	return errors.Join(failures...)
}
