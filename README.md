# git-review

A small, read-only Go CLI that asks an OpenAI-compatible model to review a Git branch. The model can inspect the repository through four bounded tools—`stat`, `glob`, `grep`, and `read`—all implemented against immutable Git objects with [`go-git`](https://github.com/go-git/go-git). The program does not invoke Git, a shell, or any other subprocess.

## Build and run

```sh
go build -o git-review .

OPENAI_API_KEY=... ./git-review --base main feature/my-change
```

Pass `-v` to log each parsed model response, including tool calls and the final response, to stderr. The final review remains on stdout, so it can still be redirected independently:

```sh
./git-review -v --base main feature/my-change >review.txt
```

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
