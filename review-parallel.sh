#!/usr/bin/env bash

set -euo pipefail

usage() {
	cat <<'EOF'
Run three focused git-review agents in parallel.

Usage:
  review-parallel.sh [--format markdown|json] [--binary PATH] [git-review flags] <branch>

Options:
  --format FORMAT  Combined output format: markdown (default) or json.
  --binary PATH    git-review binary (default: GIT_REVIEW_BIN, ./git-review,
                   or git-review from PATH).
  -h, --help       Show this help.

All other arguments are passed to each git-review process. The script supplies
focused --instructions itself, so --instructions and --instructions-file are
not accepted. Set GIT_REVIEW_INSTRUCTIONS to append common project guidance to
all three agents.
EOF
}

format=markdown
binary=${GIT_REVIEW_BIN:-}
declare -a review_args=()

while (($# > 0)); do
	case $1 in
		--format)
			if (($# < 2)); then
				echo "review-parallel: --format requires a value" >&2
				exit 2
			fi
			format=$2
			shift 2
			;;
		--format=*)
			format=${1#*=}
			shift
			;;
		--binary)
			if (($# < 2)); then
				echo "review-parallel: --binary requires a path" >&2
				exit 2
			fi
			binary=$2
			shift 2
			;;
		--binary=*)
			binary=${1#*=}
			shift
			;;
		--instructions|--instructions-file|--instructions=*|--instructions-file=*)
			echo "review-parallel: $1 is managed by this script; use GIT_REVIEW_INSTRUCTIONS for common guidance" >&2
			exit 2
			;;
		-h|--help)
			usage
			exit 0
			;;
		--)
			shift
			review_args+=("$@")
			break
			;;
		*)
			review_args+=("$1")
			shift
			;;
	esac
done

if [[ $format != markdown && $format != json ]]; then
	echo "review-parallel: format must be markdown or json" >&2
	exit 2
fi

if [[ -z $binary ]]; then
	if [[ -x ./git-review ]]; then
		binary=./git-review
	elif command -v git-review >/dev/null 2>&1; then
		binary=$(command -v git-review)
	else
		echo "review-parallel: git-review binary not found; build it or pass --binary" >&2
		exit 2
	fi
fi
if [[ ! -x $binary ]]; then
	echo "review-parallel: binary is not executable: $binary" >&2
	exit 2
fi

work_dir=$(mktemp -d "${TMPDIR:-/tmp}/git-review-parallel.XXXXXX")
declare -a pids=()
declare -a reader_pids=()

cleanup() {
	local status=$?
	trap - EXIT INT TERM
	if ((${#pids[@]})); then
		for pid in "${pids[@]}"; do
			kill "$pid" 2>/dev/null || true
		done
	fi
	if ((${#reader_pids[@]})); then
		for pid in "${reader_pids[@]}"; do
			kill "$pid" 2>/dev/null || true
		done
	fi
	rm -rf "$work_dir"
	exit "$status"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

common_instructions=${GIT_REVIEW_INSTRUCTIONS:-}
names=(security quality bugs)
titles=("Security" "Code quality" "General bugs")
prompts=(
	"Focus on security defects: authentication and authorization bypasses, injection, unsafe input handling, secret exposure, cryptographic misuse, path traversal, denial of service, and trust-boundary violations. Report only concrete defects introduced by this branch."
	"Focus on code-quality defects with operational impact: broken abstractions, resource leaks, error-handling gaps, concurrency hazards, maintainability problems likely to cause future defects, and needless complexity that obscures correctness. Do not report cosmetic style preferences."
	"Focus on general correctness: logic errors, bad edge-case handling, regressions, incorrect API behavior, data loss, compatibility problems, and failures in realistic execution paths. Report only actionable bugs introduced by this branch."
)

prefix_output() {
	local name=$1 line
	while IFS= read -r line; do
		printf '[%s] %s\n' "$name" "$line" >&2
	done
}

run_agent() {
	local index=$1 prompt=${prompts[$1]} output=$work_dir/${names[$1]}.$format log_pipe=$2
	if [[ -n $common_instructions ]]; then
		prompt="$prompt

Additional project guidance:
$common_instructions"
	fi
	"$binary" "${review_args[@]}" --format "$format" --instructions "$prompt" \
		>"$output" 2>"$log_pipe"
}

for index in 0 1 2; do
	log_pipe=$work_dir/${names[$index]}.stderr
	mkfifo "$log_pipe"
	prefix_output "${names[$index]}" <"$log_pipe" &
	reader_pids[$index]=$!
	run_agent "$index" "$log_pipe" &
	pids[$index]=$!
	echo "[${names[$index]}] started" >&2
done

failed=0
for index in 0 1 2; do
	if ! wait "${pids[$index]}"; then
		echo "review-parallel: ${names[$index]} agent failed" >&2
		failed=1
	else
		echo "[${names[$index]}] complete" >&2
	fi
done
for index in 0 1 2; do
	wait "${reader_pids[$index]}" || true
done
if ((failed)); then
	exit 1
fi

if [[ $format == json ]]; then
	printf '{\n'
	for index in 0 1 2; do
		if ((index > 0)); then
			printf ',\n'
		fi
		printf '  "%s": ' "${names[$index]}"
		sed 's/^/  /' "$work_dir/${names[$index]}.json" | sed '1s/^  //'
	done
	printf '}\n'
	exit 0
fi

printf '# Parallel review\n'
for index in 0 1 2; do
	printf '\n## %s\n' "${titles[$index]}"
	sed -e '1{/^# Review$/d;}' -e 's/^### /#### /' -e 's/^## /### /' "$work_dir/${names[$index]}.markdown"
done
