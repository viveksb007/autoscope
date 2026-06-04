# Auto Debugger — Requirements

## Problem

EKS Auto Mode runs Bottlerocket on managed nodes. No SSH, no user SSM session, no admin container by default. On-node systemd agents (kubelet, containerd, vpc-cni, ebs-csi-node, kube-proxy, plus Auto-specific monitors: StorageMonitor, KernelMonitor, NetworkingMonitor, ContainerRuntimeMonitor) are opaque to the operator. Debugging today requires hand-rolling a privileged hostPID pod and `nsenter`-ing into PID 1 — slow, error-prone, not repeatable, not LLM-driveable.

## Goal

`auto` — single Go binary that wraps that pattern behind named subcommands. One command from a laptop runs `tcpdump`, fetches Prometheus metrics, or tails systemd logs against any node in the cluster. Output is shaped for both human operators and LLM agents.

## Non-Goals (v1)

- Modifying node state (writes, restarts, killing services). Read/observe only.
- Persistent DaemonSet — every command spawns/reuses an on-demand pod.
- Distributing as kubectl plugin.
- S3 upload of artifacts.
- Custom container image — netshoot for v1.
- Web UI / TUI.

## Functional Requirements

### FR1 — `auto observe tcpdump <pod> [-n NS]`
- Capture pcap from target pod's network namespace (not host net).
- Flags: `--filter EXPR` (BPF), `--duration DUR`, `--out PATH` (default `./tcpdump-<pod>-<ts>.pcap`), `--snaplen N`, `--iface I`, `--container N`.
- Stream raw pcap → local file. Stream human-readable summary → stdout (off by default; enable with `--summary`).
- Stop conditions:
  - duration elapse (busybox `timeout -s INT` in remote)
  - Ctrl-C (SPDY-cancel; verified to produce valid pcaps)
  - target pod transitions to `Pending`/`Failed`/`Deleted` or restartCount increments — watcher cancels context
  - debug pod transitions out of `Running` — `Ensure`'s termination channel cancels context
- Pcap validity: file is `Sync()`ed before close; round-2 verified valid pcap on SPDY-cancel + busybox-timeout paths.

### FR2 — `auto metrics <agent> <node>`
- Two transport modes:
  - **apiserver-proxy** (default for kubelet): `GET /api/v1/nodes/<node>/proxy/<path>`. No debug pod needed for kubelet metrics path.
  - **node-localhost** (default for everything else, also `--via=node`): `nsenter -t 1 -n -- curl http://127.0.0.1:<port><path>` from debug pod (uses pod's mount-ns ⇒ netshoot curl).
- `<agent>` resolved against built-in catalog (see `docs/VERIFY.md` for verified entries) OR pass `--port` + `--path` for arbitrary unit.
- Default: one-shot fetch.
- `--tail INTERVAL` (default `20s`): repeat until Ctrl-C, prefix each block with timestamp.
- `--json`: emit NDJSON `{ts, agent, node, body}` per fetch.
- Catalog hit with no `metrics` endpoint AND no `--endpoint` flag → exit code 1, message lists available endpoint names (e.g. "agent 'pod-identity' has no metrics endpoint; available: healthz. Use `--endpoint healthz`."). No silent fallback.

### FR3 — `auto logs <agent> <node>`
- `nsenter -t 1 -m -u -i -- /usr/bin/journalctl -u <unit> ...`
- Modes: `--tail` (follow), `--since DUR` (default `10m`), `--lines N` (default `200`), `--grep PATTERN` (client-side regex; `journalctl --grep` not relied on across systemd versions).
- `<agent>` resolved against catalog OR pass-through unit name.
- Unit-absence probe (verified live in `VERIFY.md` round-2): `nsenter -t 1 -m -u -i -- /usr/bin/journalctl -u <unit> -n 1 --output=cat --no-pager` — stdout byte-count > 0 ⇒ unit known; == 0 ⇒ probable absent. Fallback: `find /etc/systemd/system /<arch>-bottlerocket-linux-gnu/sys-root/usr/lib/systemd/system -maxdepth 3 -name "<unit>.service"` non-empty.
- Unit absent on node → exit code 3, message `systemd unit '<unit>' not present on node <node>`.

### FR4 — `auto exec <node> -- <cmd...>`
- Generic escape hatch. Default ns set: `nsenter -t 1 -m -u -i -p -- <cmd...>` (host PID 1 namespaces minus net; net stays at hostNetwork pod default which is host net anyway).
- `--ns` flag selects custom subset: `mount,uts,ipc,net,pid,cgroup` (comma-list).
- Stdin/stdout/stderr passthrough. Exit code propagated.
- Bottlerocket's `brush` PID 1 shell rejects most utilities; agents/users must invoke direct binary paths (`/usr/bin/...`) — `auto exec` does NOT add a `sh -c` wrapper.
- Stderr banner reminding command runs as host root.

### FR5 — `auto cleanup [--node X | --all] [--yes]`
- Delete debug pods by label selector.
- Default `--all` if neither flag set; require `--yes` or interactive confirmation.

### FR6 — Session lifecycle (implicit)
- First command on a node creates a privileged debug pod, annotates with TTL epoch (now+30m).
- Subsequent commands within TTL reuse pod (deterministic name `auto-debug-<sha8(node)>`).
- TTL refreshed each `Ensure()`.
- **Best-effort opportunistic GC at startup**: every `auto` invocation lists labeled pods in the namespace and deletes those past their TTL annotation or in `Succeeded`/`Failed` phase. No background controller in v1.
- Explicit `auto cleanup` always available.

## Non-Functional Requirements

### NFR1 — Authentication & Authorization
- Caller's kubeconfig only. No installed ServiceAccount required.
- **RBAC required (cluster-admin-equivalent in effect — see SECURITY.md):**

| Resource | Verbs | Used by |
|---|---|---|
| `pods` (in target ns) | `create, get, list, delete, patch, watch` | debug pod lifecycle, TTL refresh, readiness watch |
| `pods/exec` (in target ns) | `create` | all commands |
| `nodes/proxy` | `get` | kubelet metrics + `configz` discovery |
| `namespaces` | `create, get` | `auto install` baseline |
| `namespaces` | `patch` | `auto install --auto-label` (relaxes PSA on existing ns) |

- **PSA**: target namespace must allow privileged pods — `pod-security.kubernetes.io/enforce=privileged`. `auto install` creates the namespace fresh with this label; for an existing namespace lacking the label, `auto install --auto-label` patches it (refusal mode otherwise). On clusters where caller lacks `namespaces:create`, namespace must be pre-provisioned.
- Preflight: deferred to v0.2. v0.1 surfaces RBAC errors at first kube-API call; failing fast with a clear message is the responsibility of the caller's RBAC pre-check.

### NFR2 — Output
- Human text by default.
- `--json` flag → NDJSON. Streaming commands emit one JSON object per record.
- Errors: structured `{level: "error", code: "...", msg: "...", node: "...", pod: "..."}` on stderr.

### NFR3 — Performance
- First-command latency ≤ 60s (pod scheduling).
- Reuse latency ≤ 2s.
- tcpdump throughput must keep up with 1Gbps cluster traffic without drops at 1500-byte snaplen.

### NFR4 — Safety
- All commands read-only inside host namespace by default.
- `auto exec` is the only write-capable surface — banner warns.
- TTL-labeled pods only — no orphan pods if cleanup misses.
- Refuse to operate on nodes labeled `auto.debugger/protect=true`.

### NFR5 — Agent-driveable
- `docs/TOOL.md` is the single source of truth an LLM agent reads to drive the CLI.
- Each command spec includes: synopsis, args, flags + defaults, output schema, failure modes, example.
- Exit codes documented and stable: 0 ok, 1 user-error, 2 cluster-error, 3 node-error, 130 SIGINT.

### NFR6 — Distribution
- Single static `auto` binary, `go build ./cmd/auto`.
- Linux + macOS (amd64, arm64).
- No runtime deps beyond a working kubeconfig and network reachability to API server.

## Constraints (verified live — see `docs/VERIFY.md`)

- Bottlerocket: read-only rootfs, dm-verity. PID 1 is systemd 257. Host shell is `brush` (restricted allowlist) — `which`/`command -v`/`ls` denied. Always invoke direct binary paths via `nsenter`, never wrap in `sh -c` against host PID 1.
- `nsenter -t 1 -a` enters host mount-ns where Bottlerocket has **no** `tcpdump`, `curl`, `jq`, `crictl`. Namespace flags must be split:
  - `nsenter -t 1 -m -u -i -- /usr/bin/journalctl|systemctl` — host has both (systemd 257).
  - `nsenter -t 1 -n -- <pod-bin>` — keeps netshoot mount-ns; reaches host loopback for `curl`/`tcpdump` against host services.
  - `nsenter -t <pid> -n -- <pod-bin>` — workload pod's net-ns for targeted tcpdump.
- `systemctl status|is-active|is-failed|list-units` return **Access denied** even from privileged hostPID nsenter (Bottlerocket SELinux). Liveness must be inferred via `journalctl -u <unit> --since 1m` non-empty result, not systemctl.
- containerd socket: `/run/containerd/containerd.sock`. Mount as hostPath. Host has `ctr` at `/usr/bin/ctr`; netshoot has neither `ctr` nor `crictl` for v1. PID resolution via `ctr -n k8s.io tasks list`.
- Container runtime endpoint discovery: read kubelet config via `kubectl get --raw /api/v1/nodes/<node>/proxy/configz` → `containerRuntimeEndpoint`. Cached per session.
- Auto nodes carry `CriticalAddonsOnly:NoSchedule` (and `karpenter.sh/unregistered:NoExecute` during bootstrap). Tolerate explicitly. `nodeName` set on debug pod bypasses NoSchedule kubelet admission.
- Kubelet metrics on `:10250/metrics` require bearer auth; pod SA token returns `403` (lacks `nodes/metrics`). **Default path: apiserver-proxy** — `kubectl get --raw /api/v1/nodes/<node>/proxy/metrics` (returns 704 `kubelet_*` series, governed by caller's RBAC `nodes/proxy:get`). `:10248/healthz` is plaintext liveness probe only — not metrics.
- Multi-arch: Bottlerocket sys-root path includes arch (`/aarch64-bottlerocket-linux-gnu/sys-root/...` or `/x86_64-...`). Read once per node session via `uname -m`.

## Acceptance Criteria

1. `./auto exec <node> -- /usr/bin/journalctl -u kubelet -n 5 --no-pager` returns 5 kubelet log lines (NB: `systemctl list-units` is denied on Bottlerocket — see `docs/VERIFY.md`).
2. `./auto logs kubelet <node> --lines 50` returns last 50 kubelet journal lines.
3. `./auto metrics kubelet <node>` returns >100 lines starting with `# HELP` (apiserver-proxy path).
4. `./auto metrics kubelet <node> --endpoint healthz` returns `ok`.
5. `./auto metrics ipamd <node>` returns IPAMD Prometheus metrics (node-localhost path through debug pod).
6. `./auto observe tcpdump <pod> -n <ns> --filter "port 80" --duration 10s` produces a pcap that opens in Wireshark and shows packets.
7. `./auto cleanup --all --yes` removes every debug pod created during the test.
8. `docs/TOOL.md` lets a fresh Claude session drive all commands above without reading source — measured by canned-prompt eval in `docs/COMPLIANCE.md`.
