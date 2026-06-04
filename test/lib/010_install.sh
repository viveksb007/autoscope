test_install_idempotent() {
  if auto install >/dev/null 2>&1; then
    pass "install (idempotent on existing PSA-labeled ns)"
  else
    fail "install non-zero on existing labeled ns"
  fi
}

test_install_auto_label() {
  if auto install --auto-label >/dev/null 2>&1; then
    pass "install --auto-label noop on labeled ns"
  else
    fail "install --auto-label non-zero on labeled ns"
  fi
}

test_install_namespace_psa_label() {
  local label
  label=$(kctl get ns auto-debug -o jsonpath='{.metadata.labels.pod-security\.kubernetes\.io/enforce}')
  if [[ "$label" == "privileged" ]]; then
    pass "install set PSA enforce=privileged"
  else
    fail "PSA label missing or wrong: '$label'"
  fi
}

run_in_section "install" \
  test_install_idempotent \
  test_install_auto_label \
  test_install_namespace_psa_label
