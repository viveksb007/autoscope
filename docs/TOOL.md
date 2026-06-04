# `auto` — Tool Description (Agent SOP)

This file is the **canonical CLI contract** for the `auto` command. AI agents and operators MUST treat this file as the source of truth when invoking `auto`. REQUIREMENTS.md and DESIGN.md describe motivations and internals; TOOL.md describes the surface.

## What This Tool Does

`auto` debugs EKS Auto Mode (Bottlerocket) nodes from a laptop. It creates an on-demand privileged pod on the target node and runs `tcpdump`, `journalctl`, `curl` (against host-localhost metrics), or arbitrary commands in the host's PID 1 namespace via `nsenter`. SSH and SSM are not required.

## When to Use

- Inspecting on-node systemd agents on EKS Auto nodes (kubelet, containerd, ipamd, kube-proxy, eks-node-monitoring-agent, eks-pod-identity-agent, aws-network-policy-agent, coredns, others).
- Capturing pcap traffic from a specific pod's network namespace, when standard tools (kubectl debug, ephemeral containers) are insufficient.
- Reading host-systemd journals when `kubectl logs` only shows pod logs.
- Pulling Prometheus metrics from agents that don't expose ports outside the node.

## When NOT to Use

- Production change-management without a runbook — `auto` creates a privileged pod (cluster-admin-equivalent in effect on that node).
- Long-term observability — use Prometheus / CloudWatch / OpenSearch for that. `auto` is for ad-hoc investigation.
- Persistent agents — `auto` debug pods auto-cleanup; do not depend on them living.
- Modifying node state. Use only the `auto exec` escape hatch with explicit user authorization.

## Cluster Bootstrap (one-time)

```
auto install [--namespace auto-debug] [--auto-label]
```

Creates target namespace (default `auto-debug`) labeled `pod-security.kubernetes.io/enforce=privileged`. Idempotent.

State machine:
- ns absent → create with PSA label. RBAC: `namespaces:create`.
- ns present with label → no-op.
- ns present without label, `--auto-label` set → patch. RBAC: `namespaces:patch`.
- ns present without label, `--auto-label` not set → exit 1 (refusal — protects shared namespaces).

Skip this command entirely if your operator has pre-provisioned the namespace with the PSA label.

## Global Flags

| Flag | Default | Meaning |
|---|---|---|
| `--kubeconfig PATH` | `$KUBECONFIG` or `~/.kube/config` | Standard k8s flag |
| `--context NAME` | current | Kubeconfig context |
| `--namespace NS` | `auto-debug` | Namespace where debug pod runs |
| `--image REF` | pinned netshoot digest | Override debug pod image |
| `--json` | off | Emit NDJSON; one record per event |
| `--quiet` | off | Suppress informational stderr |
| `-v / --verbose` | off | Debug-level stderr |
| `--yes` | off | Skip confirmation prompts |
| `--require-cluster-suffix STR` | empty | Refuse to operate unless current context name matches; safety guardrail |

## Exit Codes

| Code | Meaning |
|---|---|
| `0` | Success |
| `1` | User error — bad flag, missing required arg, agent has no metrics endpoint |
| `2` | Cluster error — apiserver unreachable, RBAC denial, pod scheduling failure |
| `3` | Node error — nsenter failed, unit absent, command non-zero on remote |
| `4` | Tool internal error — should never happen, file a bug |
| `130` | SIGINT |

## Commands

### `auto install`

Bootstrap target namespace.

- **Synopsis**: `auto install [--namespace NS] [--auto-label]`
- **Required args**: none
- **Optional flags**: `--namespace` (default `auto-debug`), `--auto-label` (patch missing PSA label).
- **Output (default)**: one line `namespace <NS> ready` on success.
- **Output (--json)**: `{"event":"namespace.ready","namespace":"<NS>"}`.
- **Failure modes**:
  - namespace exists with restrictive PSA and `--auto-label` not set → exit 1.
  - caller lacks `namespaces:create` and namespace absent → exit 2.
- **Example**: `auto install --namespace auto-debug`

---

### `auto exec <node> -- <cmd...>`

Run an arbitrary command in the host PID 1 namespaces.

- **Synopsis**: `auto exec <node> [--ns LIST] -- <cmd> [args...]`
- **Required args**: `<node>` (node name), `<cmd...>` (after `--`).
- **Optional flags**:
  - `--ns LIST`: comma-list of namespaces to enter, default `mount,uts,ipc,pid` (host net is always available since debug pod is hostNetwork). Valid: `mount,uts,ipc,net,pid,cgroup`.
- **Argv handling**: `<cmd...>` is passed directly to `nsenter`. **Do NOT** rely on shell builtins (`if`, `&&`, glob expansion) — Bottlerocket's host PID 1 shell `brush` rejects most utilities. Pass full binary paths: `/usr/bin/journalctl`, `/usr/bin/ctr`, etc.
- **Output (default)**: stdout/stderr passthrough. Exit code = remote exit code.
- **Output (--json)**: not applicable; binary stdout streamed.
- **Failure modes**:
  - command not found on host → exit 3, stderr: `nsenter: failed to execute '<cmd>': No such file or directory`.
  - SELinux denial → exit 3 with denial message in stderr.
- **Example**:
  ```
  auto exec i-0a449a5e52b88c278 -- /usr/bin/ctr -n k8s.io tasks list
  auto exec i-0a449a5e52b88c278 -- /usr/bin/journalctl -u kubelet -n 5 --no-pager
  ```

---

### `auto logs <agent> <node>`

Stream logs from an on-node agent. Each catalog agent has one or more **log sources** (file or systemd-journal). Default is the agent's first source — typically the most informative one.

- **Synopsis**: `auto logs <agent> <node> [--source NAME | --source list] [--tail | --since DUR | --lines N] [--grep REGEX]`
- **Required args**: `<agent>` (catalog alias OR raw unit name like `kubelet.service`), `<node>`.
- **Optional flags**:
  - `--source NAME`: catalog log source name (see Catalog below). Default: agent's first source. `--source list` enumerates available sources for the agent and exits.
  - `--tail` / `-f`: follow (`journalctl -f` for journal sources, `tail -F` for file sources).
  - `--since DUR`: `5m`, `1h`, RFC3339 — **journal sources only** (silently ignored for file sources).
  - `--lines N`: last N lines (default `200`; ignored when `--tail` set).
  - `--grep REGEX`: client-side regex filter applied to the streamed output.
- **Argument resolution**: `<agent>` → catalog lookup (see Catalog below). Unknown alias → synthesizes a `{Name: "journal", Unit: <alias>.service}` source.
- **Source resolution**:
  1. `--source list` ⇒ print sources, exit 0.
  2. `--source ""` (omitted) ⇒ agent's first source.
  3. `--source NAME` matches catalog ⇒ that source.
  4. `--source journal` for an agent w/o explicit journal entry ⇒ synthesize from agent's primary `Unit`.
  5. Else ⇒ exit 1 listing available source names.
- **Output (default)**: raw bytes from the remote process (line-delimited).
- **Failure modes**:
  - Unit absent on node (journal source) → exit 3, message: `systemd unit '<unit>' not present on node`.
  - Log file absent on node (file source) → exit 3, message: `log file '<path>' not present on node (source='<name>')`.
- **Examples**:
  ```
  auto logs kubelet i-0a449a5e52b88c278 --lines 50
  auto logs kubelet i-0a449a5e52b88c278 --tail
  auto logs network-policy i-0a449a5e52b88c278 --source list
  auto logs network-policy i-0a449a5e52b88c278 --tail            # default = network-policy-agent.log
  auto logs network-policy i-0a449a5e52b88c278 --source bpf      # ebpf-sdk.log
  auto logs network-policy i-0a449a5e52b88c278 --source journal  # journalctl (DNS proxy access log)
  auto logs ipamd i-0a449a5e52b88c278 --source plugin --grep "ADD"
  ```

---

### `auto metrics <agent> <node>`

Pull Prometheus metrics or healthz from an on-node agent.

- **Synopsis**: `auto metrics <agent> <node> [--endpoint NAME | --port N --path P] [--via=apiserver|node] [--tail INTERVAL]`
- **Required args**: `<agent>` (catalog alias), `<node>`.
- **Optional flags**:
  - `--endpoint NAME`: catalog endpoint name (`metrics`, `healthz`, `cadvisor`, etc.). Default `metrics`. **Required** if agent has no `metrics` endpoint.
  - `--port N --path P`: bypass catalog; both required together when used.
  - `--via apiserver|node`: transport override.
  - `--tail INTERVAL`: repeat fetch every interval (e.g. `20s`); Ctrl-C to stop.
- **Resolution order (deterministic)**:
  1. `--port` AND `--path` set ⇒ `(transport, port, path)` = (`node`, port, path); ignore `--endpoint` and catalog. `--via` MAY override transport to `apiserver` (uses path on apiserver-proxy URL); otherwise default `node`.
  2. `--port` XOR `--path` set ⇒ exit 1 ("--port and --path must both be set or both unset").
  3. `--endpoint NAME` matches a catalog entry for the agent ⇒ `(transport, port, path)` taken from the matching entry; `--via` overrides only the transport.
  4. `--endpoint` not set:
     - Catalog has `metrics` endpoint for the agent ⇒ use it as in step 3.
     - Catalog has no `metrics` endpoint ⇒ exit 1 (message lists available endpoints).
  5. `--endpoint NAME` set, but no catalog match for the agent ⇒ exit 1 ("agent has no endpoint named '<NAME>'; use `--port`/`--path` for arbitrary endpoints").
- **Output (default)**: Prometheus exposition format text, with timestamped block headers if `--tail`.
- **Output (--json)**: `{"event":"metrics","ts":"...","agent":"...","node":"...","endpoint":"...","body":"..."}` per fetch.
- **Failure modes**:
  - Agent has no `metrics` endpoint, no `--endpoint` flag → exit 1, message lists available endpoints.
  - Endpoint unreachable on node → exit 3 with curl exit-code in message.
  - apiserver-proxy 401/403 → exit 2 with RBAC hint.
- **Examples**:
  ```
  auto metrics kubelet i-0a449a5e52b88c278                       # apiserver-proxy /metrics
  auto metrics kubelet i-0a449a5e52b88c278 --endpoint healthz    # node-localhost :10248/healthz
  auto metrics ipamd i-0a449a5e52b88c278                         # node-localhost :61678/metrics
  auto metrics ipamd i-0a449a5e52b88c278 --tail 20s
  auto metrics pod-identity i-0a449a5e52b88c278                  # ERROR: no metrics endpoint; use --endpoint healthz
  auto metrics custom-svc i-0a449a5e52b88c278 --port 9090 --path /metrics
  ```

---

### `auto observe tcpdump <pod> [-n NS]`

Capture pcap from a target pod's network namespace.

- **Synopsis**: `auto observe tcpdump <pod> [-n NS] [--container N] [--filter EXPR] [--duration DUR] [--snaplen N] [--iface I] [--out PATH] [--summary]`
- **Required args**: `<pod>` (target pod name).
- **Optional flags**:
  - `-n / --ns-target NS`: target pod's namespace (default `default`).
  - `--container N`: target container in target pod (default first non-init; required if ambiguous).
  - `--filter EXPR`: tcpdump BPF expression (e.g. `port 80`, `host 1.2.3.4`).
  - `--duration DUR`: total capture time (default `30s`). `0` ⇒ unlimited; in this mode the busybox `timeout` wrapper is omitted and capture runs until Ctrl-C, target-pod death, or debug-pod death.
  - `--snaplen N`: tcpdump `-s` (default `262144`).
  - `--iface I`: interface inside target pod's net-ns (default `any`).
  - `--out PATH`: output pcap file (default `./tcpdump-<pod>-<ts>.pcap`).
  - `--summary`: also stream human-readable packet summary to stdout.
- **Output (default)**: progress on stderr (`captured N packets, M dropped`), final pcap path on stdout.
- **Output (--json)**: per-packet stats events on stderr (`{"event":"tcpdump.stats","captured":N,"dropped":M}`), final `{"event":"tcpdump.done","path":"...","bytes":...}` on stdout.
- **Stop conditions**: duration elapse, Ctrl-C, target pod death (deleted/restarted), debug pod death.
- **Failure modes**:
  - Target pod not found → exit 1.
  - Target pod has multiple containers and `--container` not given → exit 1.
  - Container not running → exit 3.
- **Examples**:
  ```
  auto observe tcpdump nginx -n default --filter "port 80" --duration 10s
  auto observe tcpdump checkout-7b5f -n shop --container app --duration 60s --out /tmp/checkout.pcap
  auto observe tcpdump my-pod -n default --filter "host 10.0.0.5" --summary
  ```

---

### `auto cleanup`

Delete debug pods.

- **Synopsis**: `auto cleanup [--node NODE | --all] [--ttl-only] [--yes]`
- **Required args**: none
- **Optional flags**:
  - `--node NODE`: delete only the debug pod for this node.
  - `--all`: all debug pods in target namespace (default if neither `--node` nor `--ttl-only` specified).
  - `--ttl-only`: delete only TTL-expired or terminal-phase pods; leaves fresh ones. Suppresses confirmation prompt automatically.
  - `--yes`: skip confirmation prompt (`--yes` is global). `--ttl-only` already skips it.
- **Output (default)**: one line per pod deleted: `deleted auto-debug-<sha8>` ; final summary `<N> pod(s) deleted`.
- **Output (--json)**: `{"event":"pod.deleted","name":"...","node":"..."}` per deletion.
- **Failure modes**:
  - No matching pods → exit 0 with `no debug pods found` on stderr.
  - RBAC denial → exit 2.
- **Examples**:
  ```
  auto cleanup --all --yes
  auto cleanup --node i-0a449a5e52b88c278
  auto cleanup --ttl-only
  ```

---

### `auto version`

Print version and pinned image digest.

- **Synopsis**: `auto version`
- **Output**: `auto v0.1.0 (image: nicolaka/netshoot@sha256:<digest>)`

## Catalog

Built-in agent aliases. Pass any of these as `<agent>` to `auto logs` or `auto metrics`. Unknown aliases fall through to literal unit names (you must then provide `--port`/`--path` for `auto metrics`).

### Endpoints

| Alias | Unit | metrics endpoint | healthz endpoint | Notes |
|---|---|---|---|---|
| `kubelet` | `kubelet.service` | apiserver-proxy `/metrics` | node `:10248/healthz` | Also `cadvisor`, `resource` endpoints |
| `containerd` | `containerd.service` | — | — | No stable localhost endpoint; use `journalctl` |
| `kube-proxy` | `kube-proxy.service` | node `:10249/metrics` | node `:10256/healthz` | |
| `ipamd` | `ipamd.service` | node `:61678/metrics` | — | Also `--endpoint introspect` (`:61679/v1/enis`) |
| `network-policy` | `aws-network-policy-agent.service` | node `:8900/metrics` | node `:8901/healthz` | |
| `node-monitor` | `eks-node-monitoring-agent.service` | node `:8801/metrics` | node `:8800/healthz` | |
| `pod-identity` | `eks-pod-identity-agent.service` | — (none) | node `:2703/healthz` | `auto metrics pod-identity` errors; use `--endpoint healthz` |
| `coredns` | `coredns.service` | node `:9153/metrics` | node `:8080/health` | Also `--endpoint ready` (`:8181/ready`) |
| `healthchecker` | `eks-healthchecker.service` | — | — | Logs only |
| `ebs-csi` | `eks-ebs-csi-driver.service` | — | — | Logs only |
| `ebs-csi-registrar` | `eks-ebs-csi-driver-registrar.service` | — | — | Logs only |
| `efa` | `efa-k8s-device-plugin.service` | — | — | Logs only |

### Log sources (use with `--source NAME`)

| Alias | Default source (first listed) | Other sources |
|---|---|---|
| `kubelet` | `journal` (kubelet.service) | — |
| `containerd` | `journal` (containerd.service) | — |
| `kube-proxy` | `journal` (kube-proxy.service) | — |
| `ipamd` | `ipamd` file (`/var/log/aws-routed-eni/ipamd.log`) | `plugin` (`plugin.log`), `egress-v6` (`egress-v6-plugin.log`), `journal` |
| `network-policy` | `policy` file (`/var/log/aws-routed-eni/network-policy-agent.log`) | `bpf` (`ebpf-sdk.log`), `journal` (DNS proxy access log) |
| `node-monitor` | `journal` (eks-node-monitoring-agent.service) | — |
| `pod-identity` | `journal` (eks-pod-identity-agent.service) | — |
| `coredns` | `journal` (coredns.service) | — |
| `healthchecker` / `ebs-csi*` / `efa` | `journal` | — |

All endpoints + log paths verified live on EKS Auto Standard 2026.6.3 — see `VERIFY.md`.

## Common Workflows

### "Why is my pod's egress slow?"

```
auto observe tcpdump <pod> -n <ns> --filter "host <remote-ip>" --duration 30s
# inspect pcap in Wireshark
auto logs ipamd <node> --since 5m --grep "<pod-ip>"
auto metrics ipamd <node>
```

### "kubelet keeps restarting"

```
auto logs kubelet <node> --since 30m --grep "panic\|fatal\|exited"
auto metrics kubelet <node> --endpoint healthz   # quick liveness
auto exec <node> -- /usr/bin/journalctl -u kubelet -p err --since 1h --no-pager
```

### "VPC CNI not assigning IPs"

```
auto logs ipamd <node> --tail
auto metrics ipamd <node> --endpoint introspect   # /v1/enis dump
auto metrics ipamd <node> --tail 10s              # watch ENI/IP allocation
```

### "Pod identity agent failing"

```
auto logs pod-identity <node> --since 10m
auto metrics pod-identity <node> --endpoint healthz
```

## Cleanup Contract

Every workflow MUST end with:

```
auto cleanup --all --yes
```

Or rely on TTL-based opportunistic cleanup at the next `auto` invocation. Long-running automation should explicitly cleanup after each session.

## Cost / Blast Radius

- Each `auto` command may create ONE privileged pod per target node.
- That pod has hostPID, hostNetwork, hostIPC, `privileged: true`, and `nsenter -t 1` access — **equivalent to root on the node**.
- Anyone with `pods:create` in the target namespace + a privileged-allowed PSA can do the same; `auto` does not lower the bar.
- Do not run on production nodes without a runbook and explicit authorization.
- The `--require-cluster-suffix` flag is recommended to prevent accidental cross-cluster invocations.

## Output Schemas (for agents)

When `--json` is set, every command emits NDJSON to stdout and stderr. Schema fragments:

```
{"event":"namespace.ready","namespace":"auto-debug"}
{"event":"pod.creating","name":"auto-debug-<sha8>","node":"...","reused":false}
{"event":"pod.ready","name":"...","node":"...","reused":false}
{"event":"pod.deleted","name":"...","node":"..."}
{"event":"log","ts":"<RFC3339>","node":"...","unit":"...","line":"..."}
{"event":"metrics","ts":"...","agent":"...","node":"...","endpoint":"...","body":"<full prom payload>"}
{"event":"tcpdump.stats","captured":N,"dropped":M}
{"event":"tcpdump.done","path":"...","bytes":N}
{"event":"error","code":"user|cluster|node|internal","msg":"...","node":"...","unit":"...","hint":"..."}
```

`event` is always present and uniquely identifies the schema.

## Negative Behavior (for agents)

These are things `auto` does NOT do — agents MUST not assume otherwise:

- It does NOT modify node state. `auto exec` is the only command that CAN, and only if the operator passes a write-capable command after `--`.
- It does NOT cache metrics or logs locally — every fetch is live against the node.
- It does NOT support multi-node fan-out. `<node>` arg is a single node.
- It does NOT use `crictl` (Bottlerocket lacks it). Internally uses `ctr -n k8s.io tasks list`.
- It does NOT use `systemctl status`/`is-active`/`list-units` (denied by Bottlerocket SELinux). Liveness inferred from `journalctl`.
- It does NOT exit non-zero on TTL sweep finding nothing — that's normal.
