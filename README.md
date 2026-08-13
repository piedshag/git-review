# git-review

A small, read-only Go CLI that asks an OpenAI-compatible model to review a Git branch. The model can inspect the repository through four bounded tools—`stat`, `glob`, `grep`, and `read`—all implemented against immutable Git objects with [`go-git`](https://github.com/go-git/go-git). The program does not invoke Git, a shell, or any other subprocess.

The model must finish through a validated `submit_review` tool. Every finding has a `critical`, `high`, `medium`, or `low` severity, a summary of at most 12 words, a detailed explanation, and an exact file and line. An empty submission renders an explicit “No findings” result.

## Philosophy

`git-review` follows the Unix principle of doing one thing well: it reviews the
changes introduced by a Git branch. It does not check out branches, modify the
working tree, post comments, manage pull requests, or run builds and tests.
Those jobs belong to other tools.

The command is designed to compose with those tools. The review is written to
stdout in deterministic Markdown or structured JSON, while progress and
diagnostics are written to stderr. Flags can appear on either side of the branch
name, review policy can come from a flag, file, stdin, or environment variable,
and exit behavior is suitable for CI pipelines.

The model receives capabilities, not general machine access. Its only repository
operations are the fixed `stat`, `glob`, `grep`, and `read` tools. These tools use
`go-git` to read immutable base and target commit objects; they do not read the
checked-out worktree and cannot address arbitrary revisions. The model is never
given a shell, a command-execution tool, filesystem APIs, network tools, or a way
to modify Git data. Only the host process contacts the configured model endpoint.

This boundary is intentional: even if a repository contains malicious prompt
injection, the model can only request bounded reads from the selected Git
snapshots and submit a structured review. Keep new features within that narrow
contract whenever possible, and prefer external composition over adding broader
capabilities to the reviewer.

## Build and run

```sh
go build -o git-review .

OPENAI_API_KEY=... ./git-review --base main feature/my-change
```

Flags may appear before or after the branch, so `./git-review feature/my-change -v` is valid too.

Pass `-v` for a concise activity log on stderr. It shows when the model is thinking, which Git-backed tool it requested, whether it is reading the target or base snapshot, and per-step and total token usage. The final review remains on stdout, so it can still be redirected independently:

```sh
./git-review -v --base main feature/my-change >review.txt
```

Markdown is the default output format. JSON is useful for CI annotations or any
other downstream processing:

```sh
./git-review --format json --base main feature/my-change >review.json
```

The JSON document contains `status`, `findings`, and a `stats` object with total
duration, reported tokens, and cost when available.
Provider output truncation is represented by an `inconclusive` status instead of
being mistaken for a clean review.

Markdown reviews end with the same statistics in a compact summary line. The
completion event on stderr also includes total time, tokens, and cost, so the
information remains visible with either the spinner or `-v` activity output.

Use `--instructions` to add project-specific guidance while retaining the fixed
read-only tool and structured-submission contract:

```sh
./git-review feature/my-change --instructions 'Focus on migrations and backwards compatibility.'
```

Longer instructions can come from a file or stdin, which makes the command easy
to compose with other Unix tools:

```sh
./git-review feature/my-change --instructions-file review-policy.md
generate-review-policy | ./git-review feature/my-change --instructions-file -
```

`GIT_REVIEW_INSTRUCTIONS` provides the same value through the environment.

## Parallel focused reviews

`review-parallel.sh` runs three independent reviewers concurrently: one focused
on security, one on code quality, and one on general correctness. Build the
binary first, then pass the normal review arguments to the script:

```sh
go build -o git-review .
./review-parallel.sh --base main feature/my-change >review.md
```

Progress from each reviewer is prefixed on stderr. The combined result is
written to stdout, preserving the normal Unix-friendly separation. JSON output
is also supported:

```sh
./review-parallel.sh --format json --base main feature/my-change >review.json
```

Use `--binary PATH` or `GIT_REVIEW_BIN` when the executable is elsewhere. Set
`GIT_REVIEW_INSTRUCTIONS` to append common project-specific guidance to all
three focused prompts. Because these are three independent model conversations,
expect roughly three times the tokens and cost of a single review.

When the endpoint includes `usage.cost`, the activity log reports it. Otherwise, pass the model's prices in US dollars per million tokens to estimate cost locally:

```sh
./git-review -v --input-price 0.50 --output-price 2.00 --base main feature/my-change
```

Use `--debug-model-output` when the complete parsed assistant responses are needed for troubleshooting.

For streamed responses, `-v` periodically reports raw, visible-content,
reasoning, tool-call, and opaque-metadata byte counts plus a short preview of the
latest human-readable fragment. Encrypted reasoning metadata is preserved on
assistant messages across tool turns, as required by reasoning models, but is
never printed as a preview. The default spinner intentionally uses a shorter
chunks/bytes/time status so it does not wrap and leave stale animation frames.

If reasoning text itself is not useful, `--exclude-reasoning` asks compatible endpoints such as OpenRouter to omit it from the response. The model still reasons and bills reasoning tokens, but substantially less SSE data may be transferred.

The CLI defaults to `--reasoning-effort medium`. It does not set a completion-token limit; the selected model and provider choose their natural output allowance. Use an empty value to leave reasoning effort to the provider as well:

```sh
./git-review feature/my-change --reasoning-effort high
```

If a provider still truncates a response at its own limit, the command does not retry or fail the process. It prints an explicitly inconclusive review and exits successfully so CI infrastructure remains healthy without falsely reporting a clean review. Token usage and cost from the truncated attempt are retained when the provider reports them.

The same rule applies when the overall `--timeout` expires: the result is
explicitly inconclusive and exits successfully. Other API, network, repository,
and validation errors still produce a non-zero exit.

Responses are streamed by default. This keeps long model turns active and lets `-v` report when OpenRouter is still processing or response chunks have started arriving. The configurable overall `--timeout` governs the entire review; there is no shorter per-request deadline. Use `--stream=false` for an endpoint that does not support Chat Completions streaming.

Each model turn has a 64 MiB response budget. For streamed responses this counts
all SSE bytes, including comments, field names, framing, reasoning, and provider
metadata. Increase it for unusually large responses with `--max-response-mib`;
the allowed range is 1–1024 MiB. A streamed turn is accepted only after its
`[DONE]` event, so a disconnected partial tool call cannot be executed.

When stderr is an interactive terminal, the CLI shows a progress spinner without requiring `-v`. Completed phases remain as a compact event trail. The active streaming phase shows elapsed time plus received SSE chunk and byte counts, making both continued receipt and provider pauses visible. Progress output is automatically disabled for redirected output and CI logs; use `-v` there when persistent activity logs are useful.

For a local or hosted OpenAI-compatible Chat Completions endpoint:

```sh
OPENAI_BASE_URL=http://localhost:11434/v1 \
OPENAI_MODEL=my-tool-capable-model \
./git-review feature/my-change
```

`--base` is optional when one of `origin/main`, `main`, `origin/master`, or `master` exists. The comparison starts at the merge-base, so updates made to the base after the topic branch diverged are not reviewed as topic-branch changes. Run `./git-review -h` for all flags.

## Security model

- Repository content comes from commit trees and blobs, not the checked-out filesystem.
- The model chooses only among four fixed, read-only repository functions plus the validated terminal `submit_review` function. It cannot provide commands or arbitrary revisions.
- Reads, search results, payloads, tool turns, and review duration are bounded.
- No subprocess API is used anywhere in the application.

The process still needs network access to the configured model endpoint and read access to the repository's `.git` object database. In CI, pin the built binary and dependencies, use a read-only checkout, and scope the API credential to the model service.
