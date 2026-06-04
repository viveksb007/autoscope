test_logs_lines() {
  local n
  n=$(auto logs kubelet "$TARGET_NODE" --lines 5 2>/dev/null | wc -l)
  if [[ $n -ge 5 ]]; then
    pass "logs --lines 5 ($n lines)"
  else
    fail "logs --lines 5: only $n lines"
  fi
}

test_logs_grep() {
  local n
  n=$(auto logs kubelet "$TARGET_NODE" --lines 200 --grep "kubelet" 2>/dev/null | wc -l)
  if [[ $n -gt 0 ]]; then
    pass "logs --grep ($n matching lines)"
  else
    fail "logs --grep returned 0 lines"
  fi
}

test_logs_tail_with_timeout() {
  # follow for 4s, expect at least 1 line.
  # NOTE: timeout(1) cannot invoke shell functions, must call the binary
  # directly with its global flags.
  local n
  n=$(timeout 5 "$AUTO" --context "$KCTX" --image "$IMAGE" logs kubelet "$TARGET_NODE" --tail 2>/dev/null | wc -l)
  if [[ $n -gt 0 ]]; then
    pass "logs --tail produced output ($n lines in 4s)"
  else
    fail "logs --tail produced 0 lines"
  fi
}

test_logs_source_list() {
  local out
  out=$(auto logs network-policy "$TARGET_NODE" --source list 2>/dev/null)
  if echo "$out" | grep -q "policy.*default" && echo "$out" | grep -q "bpf" && echo "$out" | grep -q "journal"; then
    pass "logs --source list shows policy/bpf/journal"
  else
    fail "--source list missing entries: $out"
  fi
}

test_logs_source_default_is_file() {
  local out
  out=$(auto logs network-policy "$TARGET_NODE" --lines 3 2>/dev/null)
  # Policy file is JSON-lines; first non-debug line should start with '{'.
  if echo "$out" | grep -q '^{'; then
    pass "logs network-policy default → JSON file source"
  else
    fail "default network-policy source not file: $(echo "$out" | head -2)"
  fi
}

test_logs_source_journal_explicit() {
  local out
  out=$(auto logs network-policy "$TARGET_NODE" --source journal --lines 3 2>/dev/null)
  # Journal lines look like 'Mon DD HH:MM:SS hostname agent[pid]: ...'.
  if echo "$out" | grep -qE '^[A-Z][a-z]{2} [0-9]{2}'; then
    pass "logs --source journal returns journalctl format"
  else
    fail "journal source format unexpected: $(echo "$out" | head -2)"
  fi
}

test_logs_source_unknown_errors() {
  if auto logs network-policy "$TARGET_NODE" --source bogus >/dev/null 2>&1; then
    fail "--source bogus returned 0"
  else
    pass "--source bogus exits non-zero"
  fi
}

test_logs_source_bpf() {
  local n
  n=$(auto logs network-policy "$TARGET_NODE" --source bpf --lines 2 2>/dev/null | wc -l)
  if [[ $n -gt 0 ]]; then
    pass "logs --source bpf"
  else
    fail "logs --source bpf empty"
  fi
}

test_logs_unknown_alias_falls_through() {
  # Unknown alias → synthesized journal source against <alias>.service.
  # We expect non-zero (unit doesn't exist) but specifically code 3 (Node).
  auto logs nonexistent-svc "$TARGET_NODE" --lines 1 >/dev/null 2>&1
  local rc=$?
  if [[ $rc -eq 3 ]]; then
    pass "logs unknown alias → exit 3 (Node) for absent unit"
  else
    fail "unknown alias rc=$rc, want 3"
  fi
}

run_in_section "logs" \
  test_logs_lines \
  test_logs_grep \
  test_logs_tail_with_timeout \
  test_logs_source_list \
  test_logs_source_default_is_file \
  test_logs_source_journal_explicit \
  test_logs_source_unknown_errors \
  test_logs_source_bpf \
  test_logs_unknown_alias_falls_through
