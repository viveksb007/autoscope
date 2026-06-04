# Shared helpers for the e2e harness. Sourced by test/e2e.sh and the
# numbered library files. Bash 4+ assumed.

# Counters live in the parent shell; sub-shells must NOT call pass/fail.
PASS_COUNT=${PASS_COUNT:-0}
FAIL_COUNT=${FAIL_COUNT:-0}
FAILED=()

# Required env vars (validated in e2e.sh).
: "${AUTO:=}"
: "${TARGET_NODE:=}"
: "${TARGET_POD:=}"
: "${NS:=}"
: "${KCTX:=}"
: "${IMAGE:=nicolaka/netshoot:latest}"

ts() { date +'%H:%M:%S'; }
log()  { printf '\033[36m[%s]\033[0m %s\n' "$(ts)" "$*" >&2; }
pass() { PASS_COUNT=$((PASS_COUNT + 1)); printf '\033[32mPASS\033[0m %s\n' "$*"; }
fail() { FAIL_COUNT=$((FAIL_COUNT + 1)); FAILED+=("$*"); printf '\033[31mFAIL\033[0m %s\n' "$*"; }

# auto wraps the binary with the standard global flags.
auto() {
  "$AUTO" --context "$KCTX" --image "$IMAGE" "$@"
}

# kctl wraps kubectl with the same context.
kctl() {
  kubectl --context "$KCTX" "$@"
}

run_in_section() {
  local name=$1; shift
  log "── $name ──"
  local fn
  for fn in "$@"; do "$fn"; done
}

# setup_namespace_and_pod creates a fresh test namespace + a single-container
# target pod pinned to TARGET_NODE. Used as the workload that `auto observe
# tcpdump` captures from.
setup_namespace_and_pod() {
  log "Creating namespace $NS"
  kctl create ns "$NS" >/dev/null

  log "Creating target pod $NS/$TARGET_POD on $TARGET_NODE"
  cat <<EOF | kctl apply -f - >/dev/null
apiVersion: v1
kind: Pod
metadata:
  name: $TARGET_POD
  namespace: $NS
  labels:
    app: e2e-target
spec:
  nodeName: $TARGET_NODE
  containers:
  - name: app
    image: $IMAGE
    command: ["/bin/sh","-c","sleep 600"]
EOF
  kctl wait -n "$NS" pod/$TARGET_POD --for=condition=Ready --timeout=90s >/dev/null
}

# setup_multi_container_pod creates a 2-container pod for the
# observe-tcpdump --container ambiguity test.
setup_multi_container_pod() {
  cat <<EOF | kctl apply -f - >/dev/null
apiVersion: v1
kind: Pod
metadata:
  name: multi-c
  namespace: $NS
spec:
  nodeName: $TARGET_NODE
  containers:
  - name: app
    image: $IMAGE
    command: ["/bin/sh","-c","sleep 600"]
  - name: proxy
    image: $IMAGE
    command: ["/bin/sh","-c","sleep 600"]
EOF
  kctl wait -n "$NS" pod/multi-c --for=condition=Ready --timeout=90s >/dev/null
}

teardown() {
  local rc=$?
  log "Tearing down"
  auto cleanup --all --yes >/dev/null 2>&1 || true
  kctl delete ns "$NS" --grace-period=0 --force --wait=false 2>/dev/null || true
  return $rc
}

summary() {
  echo
  echo "──────────────────────────────────────────"
  printf 'PASS=%d  FAIL=%d\n' "$PASS_COUNT" "$FAIL_COUNT"
  if (( FAIL_COUNT > 0 )); then
    for m in "${FAILED[@]}"; do echo "  ✗ $m"; done
    return 1
  fi
  return 0
}
