#!/bin/sh
set -eu

if [ "$#" -gt 2 ]; then
    printf 'usage: %s [new-artifact-directory [benchmark-pattern]]\n' "$0" >&2
    exit 2
fi
repo_root=$(CDPATH='' cd "$(dirname "$0")/../.." && pwd)
cd "$repo_root"
if [ "$#" -ge 1 ]; then
    mkdir "$1"
    artifacts=$(CDPATH='' cd "$1" && pwd)
else
    artifacts=$(mktemp -d /tmp/am-render.XXXXXX)
fi
pattern=${2:-'^BenchmarkFocusedSplitFrame$'}
sockets=$(mktemp -d /tmp/amprof.XXXXXX)
mkdir -m 700 "$sockets/tmux-$(id -u)" "$sockets/config" "$sockets/zsh"
unset TMUX TMUX_PANE
export TMUX_TMPDIR="$sockets" SHELL=/bin/sh ZDOTDIR="$sockets/zsh"
export AGENT_MANAGER_CONFIG_DIR="$sockets/config"
{
    go version
    go env GOOS GOARCH
    git rev-parse HEAD
    git diff --stat
    git status --short
    if [ -f internal/ui/frame_adapter_bench_test.go ]; then
        git hash-object internal/ui/frame_adapter_bench_test.go
    fi
    printf 'sockets: %s\nbenchmark: %s\n' "$sockets" "$pattern"
} > "$artifacts/environment.txt"
printf 'Profiling render benchmark; artifacts: %s\n' "$artifacts"
if go test ./internal/ui -run '^$' -bench "$pattern" -benchtime=1s -count=3 -benchmem \
    -cpuprofile "$artifacts/cpu.pprof" -memprofile "$artifacts/heap.pprof" \
    -o "$artifacts/ui.test" > "$artifacts/bench.txt" 2>&1; then
    cat "$artifacts/bench.txt"
else
    cat "$artifacts/bench.txt" >&2
    printf 'Failed; artifacts and private socket directory retained: %s\n' "$sockets" >&2
    exit 1
fi
if ! awk '$1 ~ /^Benchmark/ { found=1 } END { exit !found }' "$artifacts/bench.txt"; then
    printf 'No benchmarks matched %s\n' "$pattern" >&2
    exit 1
fi
go tool pprof -top -nodecount=30 "$artifacts/ui.test" "$artifacts/cpu.pprof" > "$artifacts/cpu-top.txt"
go tool pprof -top -alloc_space -nodecount=30 "$artifacts/ui.test" "$artifacts/heap.pprof" > "$artifacts/alloc-top.txt"
printf 'Profiles ready: %s\nPrivate socket directory retained: %s\n' "$artifacts" "$sockets"
