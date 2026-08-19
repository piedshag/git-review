package pipeline

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadResolvesAgentGraphAndInstructions(t *testing.T) {
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "security.md"), []byte("Check authentication boundaries."), 0o600); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(directory, ".git-review.toml")
	contents := `version = 1
output = "final"

[defaults]
model = "default-model"
instructions = "Only report concrete defects."
timeout = "2m"
extra_body = '{"chat_template_kwargs":{"enable_thinking":true}}'

[[agent]]
id = "security"
model = "security-model"
instructions_file = "security.md"

[[agent]]
id = "correctness"

[[agent]]
id = "final"
inputs = ["security", "correctness"]
reasoning_effort = "high"
`
	if err := os.WriteFile(configPath, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}

	config, err := Load(configPath, testRuntimeDefaults())
	if err != nil {
		t.Fatal(err)
	}
	if config.Output != "final" || len(config.Agents) != 3 {
		t.Fatalf("unexpected graph: %+v", config)
	}
	security := config.Agents[0]
	if security.Job.Model != "security-model" || security.Timeout != 2*time.Minute {
		t.Fatalf("security options were not resolved: %+v", security)
	}
	if got := string(security.Job.ExtraBody["chat_template_kwargs"]); got != `{"enable_thinking":true}` {
		t.Fatalf("security extra body was not resolved: %v", security.Job.ExtraBody)
	}
	for _, expected := range []string{"Only report concrete defects.", "Check authentication boundaries."} {
		if !strings.Contains(security.Job.Instructions, expected) {
			t.Fatalf("security instructions omitted %q: %q", expected, security.Job.Instructions)
		}
	}
	if config.Agents[1].Job.Model != "default-model" {
		t.Fatalf("agent did not inherit model: %+v", config.Agents[1])
	}
	if config.Agents[2].Job.ReasoningEffort != "high" {
		t.Fatalf("judge did not override reasoning effort: %+v", config.Agents[2])
	}
}

func TestLoadInfersSingleSink(t *testing.T) {
	configPath := writeConfig(t, `version = 1
[[agent]]
id = "reviewer"
[[agent]]
id = "judge"
inputs = ["reviewer"]
`)
	config, err := Load(configPath, testRuntimeDefaults())
	if err != nil {
		t.Fatal(err)
	}
	if config.Output != "judge" {
		t.Fatalf("output=%q, want judge", config.Output)
	}
}

func TestLoadRejectsInvalidGraphs(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		message string
	}{
		{
			name: "unknown input",
			body: `version = 1
output = "judge"
[[agent]]
id = "judge"
inputs = ["missing"]
`,
			message: "unknown input",
		},
		{
			name: "cycle",
			body: `version = 1
output = "one"
[[agent]]
id = "one"
inputs = ["two"]
[[agent]]
id = "two"
inputs = ["one"]
`,
			message: "cycle",
		},
		{
			name: "multiple sinks",
			body: `version = 1
[[agent]]
id = "one"
[[agent]]
id = "two"
`,
			message: "output is required",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Load(writeConfig(t, test.body), testRuntimeDefaults())
			if err == nil || !strings.Contains(err.Error(), test.message) {
				t.Fatalf("error=%v, want substring %q", err, test.message)
			}
		})
	}
}

func TestLoadRejectsUnknownKeys(t *testing.T) {
	_, err := Load(writeConfig(t, `version = 1
unknown = true
[[agent]]
id = "reviewer"
`), testRuntimeDefaults())
	if err == nil || !strings.Contains(err.Error(), "unknown review run config key") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestExampleConfigRemainsValid(t *testing.T) {
	config, err := Load("../../.git-review.example.toml", testRuntimeDefaults())
	if err != nil {
		t.Fatal(err)
	}
	if config.Output != "final" || len(config.Agents) != 4 {
		t.Fatalf("unexpected example graph: %+v", config)
	}
}

func testRuntimeDefaults() RuntimeDefaults {
	return RuntimeDefaults{
		Endpoint:        "https://example.test/v1",
		Model:           "runtime-model",
		Stream:          true,
		MaxResponseMiB:  64,
		ReasoningEffort: "medium",
		MaxSteps:        30,
		Timeout:         10 * time.Minute,
	}
}

func writeConfig(t *testing.T, contents string) string {
	t.Helper()
	filename := filepath.Join(t.TempDir(), ".git-review.toml")
	if err := os.WriteFile(filename, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	return filename
}
