test_metrics_kubelet_apiserver_proxy() {
  local n
  n=$(auto metrics kubelet "$TARGET_NODE" 2>/dev/null | grep -c '^kubelet_')
  if [[ $n -ge 50 ]]; then
    pass "metrics kubelet (apiserver-proxy, $n kubelet_* series)"
  else
    fail "kubelet metrics returned only $n series"
  fi
}

test_metrics_kubelet_healthz_endpoint() {
  local out
  out=$(auto metrics kubelet "$TARGET_NODE" --endpoint healthz 2>/dev/null | tail -1)
  if [[ "$out" == "ok" ]]; then
    pass "metrics kubelet --endpoint healthz"
  else
    fail "kubelet healthz returned: '$out'"
  fi
}

test_metrics_ipamd_node_localhost() {
  local n
  n=$(auto metrics ipamd "$TARGET_NODE" 2>/dev/null | grep -c '^awscni_')
  if [[ $n -gt 0 ]]; then
    pass "metrics ipamd (node-localhost, $n awscni_* series)"
  else
    fail "ipamd metrics returned 0 awscni_* series"
  fi
}

test_metrics_pod_identity_no_metrics_endpoint() {
  if auto metrics pod-identity "$TARGET_NODE" >/dev/null 2>&1; then
    fail "pod-identity metrics returned 0 (should fail-fast)"
  else
    pass "metrics pod-identity exits 1 (no metrics endpoint)"
  fi
}

test_metrics_custom_port_path() {
  # Bypass catalog: hit kubelet :10248/healthz via --port/--path.
  local out
  out=$(auto metrics nonexistent "$TARGET_NODE" --port 10248 --path /healthz 2>/dev/null | tail -1)
  if [[ "$out" == "ok" ]]; then
    pass "metrics --port/--path bypass catalog"
  else
    fail "--port/--path returned: '$out'"
  fi
}

test_metrics_port_without_path_errors() {
  if auto metrics kubelet "$TARGET_NODE" --port 10248 >/dev/null 2>&1; then
    fail "--port without --path returned 0"
  else
    pass "--port without --path exits 1"
  fi
}

test_metrics_tail() {
  # 3 fetches over 6 seconds. timeout(1) cannot call shell functions.
  local n
  n=$(timeout 6 "$AUTO" --context "$KCTX" --image "$IMAGE" metrics kubelet "$TARGET_NODE" --tail 2s 2>/dev/null | grep -c '^kubelet_active_pods')
  if [[ $n -ge 3 ]]; then
    pass "metrics --tail 2s ($n fetches in 6s)"
  else
    fail "metrics --tail 2s only $n fetches"
  fi
}

run_in_section "metrics" \
  test_metrics_kubelet_apiserver_proxy \
  test_metrics_kubelet_healthz_endpoint \
  test_metrics_ipamd_node_localhost \
  test_metrics_pod_identity_no_metrics_endpoint \
  test_metrics_custom_port_path \
  test_metrics_port_without_path_errors \
  test_metrics_tail
