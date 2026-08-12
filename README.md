# git-review

A small, read-only Go CLI that asks an OpenAI-compatible model to review a Git branch. The model can inspect the repository through four bounded tools—`stat`, `glob`, `grep`, and `read`—all implemented against immutable Git objects with [`go-git`](https://github.com/go-git/go-git). The program does not invoke Git, a shell, or any other subprocess.

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

When the endpoint includes `usage.cost`, the activity log reports it. Otherwise, pass the model's prices in US dollars per million tokens to estimate cost locally:

```sh
./git-review -v --input-price 0.50 --output-price 2.00 --base main feature/my-change
```

Use `--debug-model-output` when the complete parsed assistant responses are needed for troubleshooting.

For streamed responses, `-v` reports progress every 100 SSE chunks with raw, visible-content, reasoning, and tool-call byte counts plus a short preview of the latest fragment. Reasoning fields returned by compatible providers are preserved on assistant messages across tool turns, as required by reasoning models.

If reasoning text itself is not useful, `--exclude-reasoning` asks compatible endpoints such as OpenRouter to omit it from the response. The model still reasons and bills reasoning tokens, but substantially less SSE data may be transferred.

The CLI defaults to `--reasoning-effort medium`. It does not set a completion-token limit; the selected model and provider choose their natural output allowance. Use an empty value to leave reasoning effort to the provider as well:

```sh
./git-review feature/my-change --reasoning-effort high
```

If a provider still truncates a response at its own limit, the command does not retry or fail the process. It prints an explicitly inconclusive review and exits successfully so CI infrastructure remains healthy without falsely reporting a clean review. Token usage and cost from the truncated attempt are retained when the provider reports them.

Responses are streamed by default. This keeps long model turns active and lets `-v` report when OpenRouter is still processing or response chunks have started arriving. The configurable overall `--timeout` governs the entire review; there is no shorter per-request deadline. Use `--stream=false` for an endpoint that does not support Chat Completions streaming.

Each model turn has a 64 MiB response budget, including streamed reasoning and provider metadata. Increase it for unusually large responses with `--max-response-mib`; the allowed range is 1–1024 MiB.

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
- The model chooses only among four fixed, read-only functions. It cannot provide commands or arbitrary revisions.
- Reads, search results, payloads, tool turns, and review duration are bounded.
- No subprocess API is used anywhere in the application.

The process still needs network access to the configured model endpoint and read access to the repository's `.git` object database. In CI, pin the built binary and dependencies, use a read-only checkout, and scope the API credential to the model service.
