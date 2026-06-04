# Codex Review #1 — REQUIREMENTS / DESIGN / PLAN

Date: 2026-06-04
Reviewer: codex-cli 0.136.0 (yolo mode)
Files reviewed: `docs/REQUIREMENTS.md`, `docs/DESIGN.md`, `docs/PLAN.md`

## Findings

### BLOCKERS

- **`docs/DESIGN.md:Exec Streaming` (line 101)** — `nsenter -t 1 -a -- curl|journalctl|systemctl` enters the host mount namespace, so commands resolve against Bottlerocket host binaries, not netshoot. Fix: replace `-a` with targeted namespace flags where container tools are required, OR prove the host has every command (Bottlerocket only ships systemd/journalctl on host; no tcpdump/curl/jq).

- **`docs/DESIGN.md:Exec Streaming` (line 113)** — `crictl inspect` cannot read `/run/containerd/containerd.sock` just because the debug pod is hostNetwork. Fix: mount the socket explicitly OR call `crictl --runtime-endpoint unix:///host/run/containerd/containerd.sock` and strip `containerd://` from the ID.

- **`docs/DESIGN.md:Catalog` (line 180)** — kubelet `http://localhost:10248/metrics` is wrong; `:10248` is healthz only. Metrics are on authenticated `:10250/metrics`. Default catalog entry will fail or return non-metrics.

### HIGH

- **`docs/REQUIREMENTS.md:Constraints` (line 92)** — containerd socket path "confirmed" on one instance only. Add endpoint discovery / explicit validation.
- **`docs/DESIGN.md:Privileged Pod Spec` (line 54)** — hostPath `/` mounted writable. Undercuts "observe only". Make `ReadOnly: true` or mount only required sockets/log paths.
- **`docs/DESIGN.md:Privileged Pod Spec` (line 55)** — `/host` fallback underspecified for Bottlerocket dm-verity layout.
- **`docs/DESIGN.md:Lifecycle State Machine` (line 86)** — `auto.debugger/node=<node>` label can exceed 63-char limit on long node names. Hash the value, store full name in annotation.
- **`docs/DESIGN.md:Lifecycle State Machine` (line 86)** — list-then-create races on concurrent invocations → multiple privileged pods per node. Use deterministic pod names + server-side conflict handling, or a Lease.
- **`docs/DESIGN.md:Lifecycle State Machine` (line 87)** — "pick first" reusable pod is nondeterministic; can attach to a pod another command is using. Define ownership/session selection or per-pod busy state.
- **`docs/REQUIREMENTS.md:FR6` (line 52)** — TTL labels do not GC a running `sleep infinity` pod. Add startup cleanup, controller, or hard max-age delete.
- **`docs/DESIGN.md:Concurrency / Cancellation` (line 211)** — canceling SPDY exec stream does NOT guarantee SIGINT to remote process. Add remote wrapper that forwards signals + kills process group.
- **`docs/DESIGN.md:observe tcpdump` (line 126)** — pcap-valid-on-cancel is not guaranteed. tcpdump needs graceful signal flow OR bounded remote `timeout`; local writer must fsync/close after remote exit.
- **`docs/REQUIREMENTS.md:FR1` (line 26)** — "pod death" stop condition has no impl design. Add pod/container watch + behavior for restarts, completed pods, stale PIDs.
- **`docs/DESIGN.md:observe tcpdump` (line 119)** — multi-container pods unhandled. Require `--container`, define defaulting, validate init/ephemeral containers.
- **`docs/DESIGN.md:observe tcpdump` (line 121)** — `crictl inspect -o json | jq .info.pid` depends on jq + runtime-specific JSON path. Use Go JSON parsing or crictl template output.
- **`docs/DESIGN.md:Security Considerations` (line 219)** — `nicolaka/netshoot:latest` + privileged host root in kube-system is too risky for v1. Pin by digest, document image provenance.
- **`docs/REQUIREMENTS.md:NFR1` (line 61)** — RBAC list understates impact. Creating privileged hostPID/hostNetwork pods is cluster-admin-equivalent. Document explicitly + provide minimum ClusterRole + admission prereqs.
- **`docs/REQUIREMENTS.md:NFR4` (line 74)** — "all commands read-only" is false while pod is privileged with writable host root. Either enforce concretely or weaken the claim.

### MEDIUM

- **`docs/DESIGN.md:Privileged Pod Spec` (line 47)** — `Tolerations: [{Operator: Exists}]` broader than stated. Tolerate known Auto taints explicitly unless user opts in.
- **`docs/REQUIREMENTS.md:Constraints` (line 93)** — Auto taint note ignores that `NodeName` bypasses NoSchedule. Document kubelet admission + test against actual NoExecute.
- **`docs/REQUIREMENTS.md:Functional Requirements` (line 20)** — missing named wrappers for `top`/`ss`/`conntrack`/`iptables`/`nft`/`ipvsadm`/`ip route`/`df`. Forces agents into unsafe `auto exec`.
- **`docs/DESIGN.md:v2 Backlog` (line 226)** — `auto fetch` deferred, but pcap retrieval is already a v1 workflow. Define file-copy now or keep all captures strictly streamed.
- **`docs/DESIGN.md:Security Considerations` (line 217)** — `auto.debugger/protect=true` is the only guardrail. Add namespace/account allowlists + override flow + production model.
- **`docs/DESIGN.md:logs` (line 141)** — `journalctl --grep` availability varies by systemd version. Specify follow-mode buffering + JSON output behavior.
- **`docs/DESIGN.md:Output` (line 157)** — one `Sink` mixes byte streams, human, NDJSON, pcap. `--json` behavior ambiguous for tcpdump/log tail. Define per-command stdout/stderr/file contracts.
- **`docs/REQUIREMENTS.md:NFR5` (line 80)** — TOOL.md declared single source of truth, but REQS/DESIGN/PLAN duplicate specs. Pick one canonical, generate or validate others.
- **`docs/PLAN.md:Phase 5` (line 51)** — TOOL.md scheduled after impl. Move before; use as acceptance test fixture.
- **`docs/REQUIREMENTS.md:Acceptance Criteria` (line 103)** — "fresh Claude session drive 4 commands" not testable. Add concrete TOOL.md compliance checks.
- **`docs/DESIGN.md:Catalog` (line 179)** — vpc-cni, ebs-csi-node, kube-proxy missing from catalog; Auto monitor names unverified. Make live unit discovery a BLOCKING task before v1.

### LOW

- **`docs/PLAN.md:Risks` (line 77)** — "Bottlerocket nsenter quirks" only verifies `systemctl`. Add explicit live checks for `curl`, `journalctl`, `crictl`, tcpdump netns, signal cancellation.

## Reconciliation Plan

| # | Finding | Action |
|---|---|---|
| 1 | nsenter -a host has no tcpdump/curl/jq | Drop `-a`. Use `nsenter -t 1 -m -u -i -n -p -- ...` selectively. tcpdump/curl/crictl run inside debug pod (netshoot ships them); only `journalctl` and `systemctl` need host-mount-ns. Update DESIGN. |
| 2 | crictl socket access | Mount `/run/containerd/containerd.sock` as hostPath into `/host/run/containerd/containerd.sock`. Use `crictl --runtime-endpoint`. |
| 3 | kubelet 10248 wrong port | Catalog: `kubelet` metrics endpoint is `https://localhost:10250/metrics` with bearer-token from in-pod SA; `:10248/healthz` documented as plaintext-fallback only. |
| 4 | hardcoded socket path | Discovery: query node Status `containerRuntimeVersion` + `kubelet --container-runtime-endpoint` flag at runtime via kubelet config Z dump. |
| 5 | hostPath `/` writable | Switch to `ReadOnly: true` for `/`, plus separate writable mount only for pcap scratch dir if needed. |
| 6 | label length | Pod label `auto.debugger/node-hash=<sha8>`; full name in annotation. |
| 7 | list-then-create race | Deterministic pod name `auto-debug-<sha8(node)>`; rely on apiserver `AlreadyExists`; back off + reuse. |
| 8 | reuse "pick first" | Single deterministic pod per node; concurrent commands multiplex via separate exec streams (k8s exec is concurrency-safe). |
| 9 | TTL no GC | On every `auto` invocation, sweep pods whose TTL annotation < now and same session-owner. Background CronJob in v2. |
| 10 | SPDY cancel | Wrapper: `nsenter ... -- sh -c 'trap "kill -INT 0" TERM INT; exec <cmd> & wait'`. Plus remote `timeout(1)` for hard cap. |
| 11 | pcap flush | Use `tcpdump -G <dur> -W 1` (rotate-once) on the pod; write to a host file path; `auto cp` it back via `kubectl cp` after exit. Solves both flush + multi-container concerns. |
| 12 | pod death watch | `Ensure()` returns a `<-chan struct{}` closed when pod terminates; commands select on it. |
| 13 | multi-container | Add `--container` flag; default to first non-init container; error if ambiguous. |
| 14 | jq dep | Use `crictl inspect --output go-template --template '{{.info.pid}}'`. |
| 15 | tolerations | Tolerate `CriticalAddonsOnly:NoSchedule` + `karpenter.sh/unregistered:NoExecute` only. |
| 16 | NodeName bypass | Acknowledge in DESIGN; live verify covers it. |
| 17 | missing wrappers | Add `auto top`, `auto ss`, `auto conntrack`, `auto iptables`, `auto routes`, `auto df` to FR list (still v1). |
| 18 | fetch in v1 | Move `auto cp <node>:<path> <local>` into v1 — needed for pcap rotate-once flow. |
| 19 | netshoot:latest | Pin digest in Phase 0; document SHA. |
| 20 | RBAC understated | Add SECURITY.md section "Why this is cluster-admin-equivalent", with min ClusterRole + PSA / admission notes. |
| 21 | "read-only" claim | Reword NFR4: "Default operations are read-only on host state; `auto exec` and write-capable subcommands explicitly noted." |
| 22 | protect-label only | Add `--require-label key=value` global flag + config file allowlist of clusters/namespaces. |
| 23 | journalctl --grep | Drop server-side `--grep`; always client-side regex on streamed lines. |
| 24 | Sink abstraction | Split into `EventSink` (NDJSON-able) + `RawStream` (bytes). pcap is RawStream-only; `--json` no-op for tcpdump. |
| 25 | duplication | TOOL.md becomes canonical. REQUIREMENTS keeps WHY only; DESIGN keeps HOW; PLAN keeps WHEN. Schemas live in TOOL.md. |
| 26 | TOOL.md before impl | Reorder PLAN: TOOL.md is Phase 1 deliverable, used as fixture. |
| 27 | testable acceptance | Add 5 scripted prompts + expected command sequences in TOOL.md `## Compliance Tests`. |
| 28 | Catalog completeness | Phase 6 step 1 BLOCKS Phase 3+. Reorder PLAN. |
| 29 | nsenter quirks | Phase 6 step 1 expanded to enumerate every host binary used. |

## Decision: Apply All Findings

All BLOCKER + HIGH findings will be folded into the docs before implementation begins. MEDIUMs accepted unless flagged otherwise during reconciliation.
