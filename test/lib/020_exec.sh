test_exec_uname() {
  local out
  out=$(auto exec "$TARGET_NODE" -- /usr/bin/uname -s 2>/dev/null | tail -1)
  if [[ "$out" == "Linux" ]]; then
    pass "exec /usr/bin/uname -s"
  else
    fail "exec uname returned: '$out'"
  fi
}

test_exec_ctr_task_list() {
  local out
  out=$(auto exec "$TARGET_NODE" -- /usr/bin/ctr -n k8s.io tasks list 2>/dev/null | grep -c "RUNNING")
  if [[ $out -gt 0 ]]; then
    pass "exec ctr tasks list ($out RUNNING)"
  else
    fail "ctr tasks list found 0 RUNNING containers"
  fi
}

test_exec_nonzero_propagated() {
  if auto exec "$TARGET_NODE" -- /bin/false >/dev/null 2>&1; then
    fail "exec /bin/false returned 0"
  else
    pass "exec exit code propagated for /bin/false"
  fi
}

test_exec_missing_binary_node_error() {
  # Bottlerocket: /nope/bin/missing won't exist; nsenter fails.
  if auto exec "$TARGET_NODE" -- /nope/bin/missing >/dev/null 2>&1; then
    fail "exec missing binary returned 0"
  else
    pass "exec missing binary errored"
  fi
}

run_in_section "exec" \
  test_exec_uname \
  test_exec_ctr_task_list \
  test_exec_nonzero_propagated \
  test_exec_missing_binary_node_error
