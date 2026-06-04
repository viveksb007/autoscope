# autoscope — `auto`

Laptop-driven on-node debugger for **EKS Auto Mode** (Bottlerocket).

EKS Auto nodes have no SSH and no user-accessible SSM session. `auto` spawns a privileged hostPID pod on the target node and exposes `tcpdump`, `journalctl`, host-localhost metrics, and arbitrary host-PID-1 commands behind a small subcommand surface.

## Quick start

```sh
# build
go build -o auto ./cmd/auto

# bootstrap target namespace once per cluster (PSA: privileged)
./auto install

# tail kubelet journal
./auto logs kubelet i-0a449a5e52b88c278 --lines 50

# pull kubelet metrics via apiserver-proxy
./auto metrics kubelet i-0a449a5e52b88c278

# pull IPAMD metrics from on-node localhost
./auto metrics ipamd i-0a449a5e52b88c278

# capture pcap from pod's network namespace
./auto observe tcpdump my-pod -n default --filter "port 80" --duration 30s

# cleanup
./auto cleanup --all --yes
```

## Commands

| Command | Purpose |
|---|---|
| `auto install` | Create the `auto-debug` namespace with PSA `privileged` label |
| `auto exec <node> -- <cmd>` | Run command in host PID-1 namespaces |
| `auto logs <agent> <node>` | journalctl: `--tail` / `--since` / `--lines` / `--grep` |
| `auto metrics <agent> <node>` | Prometheus / healthz: `--endpoint` / `--port` / `--path` / `--tail` |
| `auto observe tcpdump <pod>` | pcap from pod netns: `--filter` / `--duration` / `--container` |
| `auto cleanup` | Delete debug pods: `--all` / `--node` / `--ttl-only` / `--yes` |
| `auto version` | Print version and pinned image digest |

Full contract: [docs/TOOL.md](docs/TOOL.md). Agent-driving prompts: [docs/COMPLIANCE.md](docs/COMPLIANCE.md).

## How it works

```
┌────────────┐         ┌────────────────┐        ┌──────────────────────────┐
│ auto CLI   │─ exec ─▶│ kube-apiserver │─ SPDY ▶│ privileged debug pod     │
│ (laptop)   │         │ caller's RBAC  │        │ netshoot (digest-pinned) │
│            │◀──────  │                │ ◀──────│ hostPID + hostNetwork    │
└────────────┘         └────────────────┘        │ /run/containerd.sock     │
                                                 └────┬────────────┬────────┘
                                                      │            │
                          nsenter -t 1 -m -u -i ──────┘            └── nsenter -t 1 -n
                          (host MOUNT-ns: journalctl, ctr)             (host NET-ns,
                                                                        pod MOUNT-ns:
                                                                        curl, tcpdump)

                          nsenter -t <wpid> -n
                          (workload pod's net-ns: tcpdump for that pod)
```

- One privileged debug pod per node (deterministic name `auto-debug-<sha8(node)>`)
- 30-minute rolling TTL refreshed on every command
- Opportunistic GC at startup
- `ctr -n k8s.io tasks list` for workload PID resolution (Bottlerocket lacks `crictl`)
- `journalctl -u <unit> -n 1 --output=cat` byte-count for unit-existence probe (Bottlerocket SELinux blocks `systemctl status`)
- busybox `timeout -s INT -k 2 <dur>` for tcpdump duration cap (netshoot has busybox, not GNU)

## Requirements

- Caller's kubeconfig
- RBAC: `pods` create/get/list/delete, `pods/exec` create, `pods/log` get, `nodes/proxy` get, `namespaces` create (+ `patch` if `--auto-label`), `selfsubjectaccessreviews` create
- Cluster admits privileged pods in target namespace (PSA `privileged`)
- Network reachability to API server

## Distribution

Single static Go binary. `go build ./cmd/auto`. Linux + macOS, amd64 + arm64.

## Security

`auto` creates a privileged hostPID pod on the target node. **This is effectively cluster-admin equivalent on that node** — anyone with `pods:create` in a privileged-allowed namespace can do the same. See [docs/SECURITY.md](docs/SECURITY.md).

Guardrails:

- `--require-cluster-suffix STR` refuses unless the active context name ends with the suffix
- Default namespace `auto-debug` (PSA-labeled) keeps blast radius scoped
- Image pinned by SHA digest (override via `--image`)

## Project status

MVP shipping. Live-verified on EKS Auto Standard 2026.6.3 (Bottlerocket, kernel 6.12.88, arm64). See [docs/VERIFY.md](docs/VERIFY.md).

## Documents

- [docs/REQUIREMENTS.md](docs/REQUIREMENTS.md) — FR/NFR, acceptance criteria
- [docs/DESIGN.md](docs/DESIGN.md) — architecture, pod spec, lifecycle, per-command flow
- [docs/PLAN.md](docs/PLAN.md) — phased implementation plan
- [docs/TOOL.md](docs/TOOL.md) — canonical CLI contract (agent-readable)
- [docs/COMPLIANCE.md](docs/COMPLIANCE.md) — 12 prompts to score TOOL.md
- [docs/VERIFY.md](docs/VERIFY.md) — live verification transcripts
- [docs/SECURITY.md](docs/SECURITY.md) — blast radius, RBAC, guardrails
- [docs/REVIEW-codex-{01,02,03}.md](docs/) — adversarial review history
