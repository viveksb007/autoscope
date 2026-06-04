_generate_traffic_in_target() {
  # Fire a few DNS queries inside the target pod so tcpdump has something to
  # capture. Runs in the background; sleeps cover capture startup latency.
  (
    sleep 1
    for _ in 1 2 3 4 5; do
      kctl exec -n "$NS" "$TARGET_POD" -- timeout 1 nslookup amazon.com >/dev/null 2>&1 || true
      sleep 0.5
    done
  ) &
  TRAFFIC_PID=$!
}

_wait_traffic() {
  if [[ -n "${TRAFFIC_PID:-}" ]]; then
    wait "$TRAFFIC_PID" 2>/dev/null || true
    unset TRAFFIC_PID
  fi
}

test_observe_default_duration() {
  local pcap stdout_out
  _generate_traffic_in_target
  stdout_out=$(auto observe tcpdump "$TARGET_POD" -n "$NS" --duration 5s --filter "udp port 53 or tcp port 80" 2>&1)
  _wait_traffic
  pcap=$(echo "$stdout_out" | awk '/saved/ {print $NF}')
  if [[ -z "$pcap" || ! -f "$pcap" ]]; then
    fail "observe pcap missing: '$pcap'"
    return
  fi
  local size; size=$(stat -c%s "$pcap")
  # Valid pcap = 24-byte header minimum. We don't assert >24 because filter
  # may match nothing; we only require the file is a recognizable pcap.
  if file "$pcap" | grep -q "tcpdump capture"; then
    pass "observe tcpdump bounded duration (pcap valid, ${size} bytes)"
  else
    fail "pcap header invalid: $(file "$pcap")"
  fi
  rm -f "$pcap"
}

test_observe_unlimited_duration_ctrlc() {
  # --duration 0 omits busybox timeout. SPDY-cancel on Ctrl-C produces a
  # valid pcap, but the remote tcpdump may keep running until the API server
  # exec timeout closes the stream (default ~10min). We work around that by
  # bounding `wait` on the auto process with a hard timeout.
  local pcap="/tmp/auto-e2e-unlimited.pcap"
  rm -f "$pcap"
  _generate_traffic_in_target
  "$AUTO" --context "$KCTX" --image "$IMAGE" \
    observe tcpdump "$TARGET_POD" -n "$NS" --duration 0 --filter "udp port 53" --out "$pcap" \
    >/dev/null 2>&1 &
  local pid=$!
  sleep 4
  kill -INT "$pid" 2>/dev/null
  # Bound the wait — if SPDY doesn't close fast, force-kill the auto pid so
  # the test doesn't stall the suite.
  for _ in 1 2 3 4 5; do
    kill -0 "$pid" 2>/dev/null || break
    sleep 1
  done
  kill -KILL "$pid" 2>/dev/null
  wait "$pid" 2>/dev/null
  _wait_traffic
  if [[ -f "$pcap" ]] && file "$pcap" | grep -q "tcpdump capture"; then
    pass "observe --duration 0 + Ctrl-C produces valid pcap"
  else
    fail "unlimited mode pcap invalid"
  fi
  rm -f "$pcap"
}

test_observe_multi_container_ambiguous_errors() {
  if auto observe tcpdump multi-c -n "$NS" --duration 1s >/dev/null 2>&1; then
    fail "multi-container w/o --container returned 0"
  else
    pass "multi-container ambiguity exits non-zero"
  fi
}

test_observe_multi_container_explicit() {
  local pcap
  pcap=$(auto observe tcpdump multi-c -n "$NS" --container proxy --duration 3s 2>&1 \
         | awk '/saved/ {print $NF}')
  if [[ -z "$pcap" || ! -f "$pcap" ]]; then
    fail "multi-c --container proxy: pcap missing ('$pcap')"
    return
  fi
  if file "$pcap" | grep -q "tcpdump capture"; then
    pass "observe --container proxy on multi-container pod"
  else
    fail "multi-c --container proxy: pcap header invalid"
  fi
  rm -f "$pcap"
}

run_in_section "observe tcpdump" \
  test_observe_default_duration \
  test_observe_unlimited_duration_ctrlc \
  test_observe_multi_container_ambiguous_errors \
  test_observe_multi_container_explicit
