test_completion_bash() {
  local n
  n=$("$AUTO" completion bash 2>/dev/null | wc -l)
  if [[ $n -gt 100 ]]; then
    pass "completion bash ($n lines)"
  else
    fail "completion bash too short: $n lines"
  fi
}

test_completion_zsh_starts_with_compdef() {
  local first
  first=$("$AUTO" completion zsh 2>/dev/null | head -1)
  if [[ "$first" == "#compdef auto" ]]; then
    pass "completion zsh starts with #compdef"
  else
    fail "completion zsh first line: '$first'"
  fi
}

test_completion_fish() {
  local n
  n=$("$AUTO" completion fish 2>/dev/null | wc -l)
  if [[ $n -gt 50 ]]; then
    pass "completion fish ($n lines)"
  else
    fail "completion fish too short: $n lines"
  fi
}

test_dynamic_agent_completion() {
  # cobra completion protocol: `__complete logs ''` returns alias\tdesc list.
  local out
  out=$("$AUTO" __complete logs '' 2>/dev/null)
  if echo "$out" | grep -q "^kubelet" && echo "$out" | grep -q "^network-policy"; then
    pass "__complete agent list includes kubelet + network-policy"
  else
    fail "__complete missing aliases: $out"
  fi
}

test_dynamic_node_completion() {
  local out
  out=$(auto __complete logs kubelet '' 2>/dev/null)
  if echo "$out" | grep -q "^$TARGET_NODE$"; then
    pass "__complete node list includes $TARGET_NODE"
  else
    fail "__complete node list missing $TARGET_NODE: $out"
  fi
}

run_in_section "completion" \
  test_completion_bash \
  test_completion_zsh_starts_with_compdef \
  test_completion_fish \
  test_dynamic_agent_completion \
  test_dynamic_node_completion
