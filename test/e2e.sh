#!/usr/bin/env bash
# End-to-end smoke harness for `auto` against a live EKS Auto cluster.
#
# Usage:
#   AUTO=./auto \
#   KCTX=<your-kubeconfig-context> \
#   TARGET_NODE=i-0a449a5e52b88c278 \
#   test/e2e.sh
#
# Optional:
#   IMAGE=nicolaka/netshoot:latest          # debug pod image override
#
# Exits non-zero if any test fails. Always cleans up created namespace +
# debug pods via trap.

# NOTE: do NOT enable pipefail. Many tests pipe a `timeout` invocation into
# `wc -l`/grep; SIGTERM from timeout makes the pipeline propagate 124,
# masking the actual line count we're trying to assert on.
set -u

# Resolve repo root regardless of cwd.
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "$HERE/.." && pwd)"

: "${AUTO:=$ROOT/auto}"
: "${KCTX:?KCTX must be set (kubeconfig context name)}"
: "${TARGET_NODE:?TARGET_NODE must be set}"
: "${IMAGE:=nicolaka/netshoot:latest}"

if [[ ! -x "$AUTO" ]]; then
  echo "auto binary not found or not executable at $AUTO" >&2
  echo "build with: go build -o auto ./cmd/auto" >&2
  exit 1
fi

NS="auto-e2e-$RANDOM"
TARGET_POD="e2e-target"

export AUTO KCTX TARGET_NODE TARGET_POD NS IMAGE

# shellcheck source=test/lib/common.sh
source "$ROOT/test/lib/common.sh"

trap teardown EXIT

# Sanity: the active context must reach the cluster.
log "Validating cluster reachability via context $KCTX"
if ! kctl get nodes "$TARGET_NODE" >/dev/null 2>&1; then
  fail "Cannot reach node $TARGET_NODE via context $KCTX"
  summary
fi

setup_namespace_and_pod
setup_multi_container_pod

# Run sections in numeric order.
for f in "$ROOT"/test/lib/0*_*.sh; do
  # shellcheck source=/dev/null
  source "$f"
done

summary
