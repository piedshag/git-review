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

GitHub Actions tests the tagged commit, builds a Linux amd64 binary, generates a
checksum, and publishes it as a GitHub Release.

## Parallel focused reviews

`review-parallel.sh` runs three reviewers concurrently, focused on security,
code quality, and general correctness:

```sh
./review-parallel.sh --base main feature/my-change >review.md
./review-parallel.sh --format json --base main feature/my-change >review.json
```

Use `--binary PATH` or `GIT_REVIEW_BIN` when the executable is elsewhere. The
script runs three independent model conversations, so expect roughly three
times the token usage and cost of a single review.

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
- `internal/review` owns the review loop, validation, limits, and output formats.
- `internal/report` renders progress and verbose logs.
