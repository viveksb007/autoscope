# Each cleanup test re-creates the debug pod (via a cheap exec) so the next
# test has something to clean up. This is the only section where we
# actively destroy the auto-debug pod.

ensure_debug_pod() {
  auto exec "$TARGET_NODE" -- /usr/bin/uname >/dev/null 2>&1
}

count_debug_pods() {
  kctl get pods -n auto-debug -l auto.debugger/session --no-headers 2>/dev/null | wc -l
}

test_cleanup_node() {
  ensure_debug_pod
  local before after
  before=$(count_debug_pods)
  auto cleanup --node "$TARGET_NODE" --yes >/dev/null 2>&1
  after=$(count_debug_pods)
  if (( after < before )); then
    pass "cleanup --node deleted $((before - after)) pod(s)"
  else
    fail "cleanup --node: count $before → $after"
  fi
}

test_cleanup_ttl_only_noop() {
  ensure_debug_pod
  # Fresh pod has unexpired TTL → expect 0 deletions.
  local out
  out=$(auto cleanup --ttl-only 2>&1 | tail -1)
  if [[ "$out" == "0 pod(s) deleted" ]]; then
    pass "cleanup --ttl-only no-op on fresh pod"
  else
    fail "ttl-only on fresh pod: '$out'"
  fi
}

test_cleanup_all_yes() {
  ensure_debug_pod
  auto cleanup --all --yes >/dev/null 2>&1
  local n
  n=$(count_debug_pods)
  if [[ $n -eq 0 ]]; then
    pass "cleanup --all --yes left 0 pods"
  else
    fail "cleanup --all left $n pods"
  fi
}

run_in_section "cleanup" \
  test_cleanup_node \
  test_cleanup_ttl_only_noop \
  test_cleanup_all_yes
