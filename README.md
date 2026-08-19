# git-review

A read-only Go CLI that uses an OpenAI-compatible model to review the changes
introduced by a Git branch.

The model inspects immutable Git objects through five bounded tools: `stat`,
`diff`, `glob`, `grep`, and `read`. It cannot access the working tree, run
commands, or modify the repository. Reviews are returned as Markdown or
structured JSON with validated findings and severity levels.

## Build and run

```sh
go build -o git-review .
go build -o git-review-run ./cmd/git-review-run

OPENAI_API_KEY=... ./git-review --base main feature/my-change
```

`--base` may be omitted when `origin/main`, `main`, `origin/master`, or `master`
exists. The comparison starts at the merge-base. Flags may appear before or
after the branch name.

Markdown is written to stdout by default, so it can be redirected:

```sh
./git-review -v --base main feature/my-change >review.md
```

Use JSON for CI or other downstream processing:

```sh
./git-review --format json --base main feature/my-change >review.json
```

JSON output includes the review status, findings, duration, token usage, and
cost when available. Incomplete reviews caused by time, turn, or provider output
limits are reported as `inconclusive`, rather than as clean reviews.

## Configuration

Add project-specific guidance with a flag, file, stdin, or environment variable:

```sh
./git-review feature/my-change \
  --instructions 'Focus on migrations and backwards compatibility.'

./git-review feature/my-change --instructions-file review-policy.md
generate-review-policy | ./git-review feature/my-change --instructions-file -
```

`GIT_REVIEW_INSTRUCTIONS` provides the same setting through the environment.

The CLI works with local or hosted OpenAI-compatible Chat Completions endpoints:

```sh
OPENAI_BASE_URL=http://localhost:11434/v1 \
OPENAI_MODEL=my-tool-capable-model \
./git-review feature/my-change
```

Useful options include:

- `-v` writes model activity and token usage to stderr.
- `--reasoning-effort` defaults to `medium`.
- `--exclude-reasoning` reduces streamed reasoning data when supported.
- `--extra-body` merges a JSON object into every request body, for provider
  controls that are not part of the Chat Completions schema - vLLM and SGLang
  read `chat_template_kwargs`, so
  `--extra-body '{"chat_template_kwargs":{"enable_thinking":true}}'` is how a
  Qwen-style model is asked to think. Also settable as `GIT_REVIEW_EXTRA_BODY`.
  Extra keys override the optional fields the client sets, such as
  `reasoning_effort`; `model`, `messages`, `tools`, `stream` and
  `stream_options` are refused.
- `--stream=false` supports endpoints without streaming.
- `--timeout`, `--max-steps`, and `--max-response-mib` control resource limits.
- `--input-price` and `--output-price` estimate cost when the endpoint does not
  report it.
- `--debug-model-output` prints parsed model responses for troubleshooting.

Run `./git-review -h` for the complete option list.

## Releasing

Push a semantic-version tag to run the release workflow:

```sh
git tag v0.1.0
git push origin v0.1.0
```

GitHub Actions tests the tagged commit, builds the Linux amd64 binaries,
generates a checksum, and publishes them as a GitHub Release.

## Composable multi-agent reviews

`git-review-run` reads a small agent graph from `.git-review.toml`. Agents with
no inputs review the same resolved Git snapshot concurrently. Agents with
inputs act as judges: they receive the named upstream reviews as untrusted
claims and can use the same read-only Git tools to verify and consolidate them.

Start with the included example:

```sh
cp .git-review.example.toml .git-review.toml
./git-review-run --base main feature/my-change >review.md
./git-review-run --format json --base main feature/my-change >review-run.json
```

### Runner configuration

The top level accepts these keys:

| Key | Required | Meaning |
| --- | --- | --- |
| `version` | Yes | Configuration schema version. The only supported value is `1`. |
| `output` | Usually | ID of the agent rendered as Markdown. It may be omitted when the graph has exactly one sink. |

Put shared settings in `[defaults]` and override them in any `[[agent]]`.
Agent settings take precedence over defaults, which take precedence over the
environment and built-in values.

| Key | Default | Meaning |
| --- | --- | --- |
| `model` | `OPENAI_MODEL`, then `gpt-5` | Model name for this agent. |
| `endpoint` | `OPENAI_BASE_URL`, then `https://api.openai.com/v1` | OpenAI-compatible API base URL. |
| `reasoning_effort` | `medium` | `none`, `minimal`, `low`, `medium`, `high`, `xhigh`, or `max`. |
| `extra_body` | `GIT_REVIEW_EXTRA_BODY` | JSON object encoded as a TOML string and merged into provider requests. Core request fields cannot be replaced. |
| `timeout` | `10m` | Go duration limiting this agent, such as `90s` or `15m`. |
| `max_steps` | `30` | Maximum model/tool turns, from 1 to 100. |
| `stream` | `true` | Whether to request streaming Chat Completions responses. |
| `max_response_mib` | `64` | Maximum response size per turn, from 1 to 1024 MiB. |
| `exclude_reasoning` | `false` | Ask compatible providers not to return reasoning text. |
| `input_price` | `0` | Input price in US dollars per million tokens, used for cost estimates. |
| `output_price` | `0` | Output price in US dollars per million tokens, used for cost estimates. |
| `instructions` | Empty | Inline additional instructions. TOML multiline strings are supported. |
| `instructions_file` | Empty | File containing additional instructions. Mutually exclusive with `instructions` in the same section. |

Instruction-file paths are resolved relative to the TOML file, may be absolute,
and are limited to 1 MiB. Stdin (`-`) is intentionally unsupported because
agents run concurrently. Instructions from `[defaults]` are applied to every
agent; each agent's own instructions or instruction file is appended to them.

```toml
[defaults]
instructions_file = ".git-review/common.md"

[[agent]]
id = "security"
model = "security-model"
instructions_file = ".git-review/security.md"
```

Every `[[agent]]` also supports:

| Key | Required | Meaning |
| --- | --- | --- |
| `id` | Yes | Unique ID of at most 64 characters, beginning with a letter and containing only letters, digits, `_`, or `-`. |
| `inputs` | No | IDs of upstream reviews to adjudicate. An agent without inputs performs an independent review. |

A configuration may contain up to 32 agents. Duplicate IDs, unknown inputs,
cycles, unknown configuration keys, and invalid option values are rejected
before any model request is made. API credentials are not stored in TOML; all
agents currently use `OPENAI_API_KEY`.

The top-level `output` selects the review rendered as Markdown. JSON retains
every node result, its inputs, model, errors, and the resolved base and target
commit hashes.

The runner command supports `--config`, `--repo`, `--base`, `--format`, `-v`,
and `--debug-model-output`. Run `git-review-run -h` for their defaults and full
descriptions.

The graph is composable: a node without `inputs` is an independent reviewer,
while a node with `inputs` adjudicates those reviews and emits the same
validated review type. Cycles, duplicate IDs, and missing inputs are rejected
before any model request is made.

Progress is always written to stderr. Interactive runs show coarse per-agent
lifecycle events. `-v` adds model and tool activity prefixed by agent ID; CI
and redirected runs remain quiet by default.

## Review behavior

The model must finish by calling a validated `submit_review` tool. A review
contains a change summary, strengths, weaknesses, and findings with severity,
explanation, file, and line information.

Responses stream by default. The CLI bounds reads, model responses, tool turns,
and total review time. It reserves time for a final submission and makes one
recovery attempt after a truncated or oversized response. Interactive runs show
a spinner; redirected and CI output stays quiet unless `-v` is used.

## Security

- Repository content is read from commit trees and blobs, not the working tree.
- The model has no shell, filesystem, network, or arbitrary-revision tools.
- Repository reads and model interactions are bounded.
- The program does not invoke subprocesses.

The process itself needs network access to the model endpoint and read access to
the repository's `.git` object database.

## Code organization

- `internal/agent` defines model, tool, usage, and reporting contracts.
- `internal/gitrepo` provides bounded read-only Git operations.
- `internal/openai` implements the Chat Completions transport.
- `internal/pipeline` loads and executes composable multi-agent review graphs.
- `internal/review` owns the review loop, validation, limits, and output formats.
- `internal/reviewapp` is the shared application service used by both binaries.
- `internal/report` renders progress and verbose logs.
