test_require_cluster_suffix_match() {
  local suffix="${KCTX##*@}"
  # Suffix gate runs in LoadSession; `version` is offline and bypasses it.
  # Use a kube-touching command (cleanup --ttl-only is fast and read-only-ish).
  if "$AUTO" --context "$KCTX" --require-cluster-suffix "$suffix" cleanup --ttl-only >/dev/null 2>&1; then
    pass "--require-cluster-suffix matching suffix"
  else
    fail "matching suffix unexpectedly errored"
  fi
}

test_require_cluster_suffix_mismatch() {
  if "$AUTO" --context "$KCTX" --require-cluster-suffix "WRONG.suffix.invalid" cleanup --ttl-only >/dev/null 2>&1; then
    fail "mismatched suffix returned 0"
  else
    pass "--require-cluster-suffix mismatch exits non-zero"
  fi
}

test_version_works_without_kube() {
  # `version` MUST NOT require kubeconfig.
  if HOME=/nope/no/kubeconfig "$AUTO" version >/dev/null 2>&1; then
    pass "version works without kubeconfig"
  else
    fail "version errored without kubeconfig"
  fi
}

run_in_section "guardrails" \
  test_require_cluster_suffix_match \
  test_require_cluster_suffix_mismatch \
  test_version_works_without_kube
