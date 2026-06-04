# Auto Debugger — Design

## Architecture

```
+------------------+        +-------------------+        +-----------------------------+
|  auto CLI (Go)   | -----> |  kube-apiserver   | -----> | privileged debug pod        |
|  cmd/auto/*      |  RBAC  |  (caller's creds) |  exec  | netshoot @ pinned digest    |
|  (laptop)        |        |                   |        | hostPID + hostNetwork       |
|                  |        |  /api/v1/nodes/   |        | hostIPC + privileged        |
|                  | <----  |  <node>/proxy/... | metrics| containerd.sock hostPath    |
+------------------+        +-------------------+        +-----+-----------------+-----+
                                                               |                 |
                          nsenter -t 1 -m -u -i -- (host MOUNT-ns)               |
                          (journalctl, /usr/bin/ctr, find /etc/systemd/...)      |
                                                               |                 |
                                                               v                 v
                                       +---------------------------+   nsenter -t 1 -n -- (host NET-ns,
                                       | Bottlerocket host (PID 1) |   pod MOUNT-ns; uses netshoot's
                                       | systemd 257               |   curl/tcpdump against 127.0.0.1)
                                       | brush shell (allowlist)   |
                                       | /usr/bin/{journalctl,ctr} |   nsenter -t <wpid> -n -- (workload pod's
                                       | /run/containerd/...sock   |   net-ns, pod MOUNT-ns; tcpdump on pod
                                       +---------------------------+   traffic)
```

Three nsenter modes, used selectively per-command (see Exec Streaming). Apiserver-proxy used for kubelet metrics — no debug pod required for that path.

## Layers

```
cmd/auto/main.go              -- thin: cobra exec, exit code mapping
internal/cli/                 -- one file per subcommand (flag parsing only)
internal/debugpod/            -- Ensure/Cleanup/ExecStream — the only layer that touches k8s API
internal/nsenter/             -- Builds nsenter -t 1 -a -- argv slices; pure functions
internal/tcpdump/             -- Pcap capture orchestration; talks to debugpod.ExecStream
internal/catalog/             -- Static map: alias → unit + metrics endpoint
internal/output/              -- human + json sinks; commands write through these
internal/kube/                -- clientset construction, kubeconfig loading
```

Dependency direction: cli → {debugpod, tcpdump, catalog, output}; debugpod → kube; nsenter has no deps. catalog has no deps. No cycles.

## Privileged Pod Spec

Built once in `debugpod.buildPodSpec(node, ns, imageDigest, runtimeSocket string) *corev1.Pod`. The `runtimeSocket` value is supplied by `NodeCache.ContainerRuntimeEndpoint(node)` (see Container Runtime Endpoint Discovery below). Live-verified manifest in `docs/VERIFY.md`.

```go
HostPID: true; HostNetwork: true; HostIPC: true
NodeName: <node>
Tolerations: [
  {Key: "CriticalAddonsOnly", Operator: Exists, Effect: NoSchedule},
  {Key: "karpenter.sh/unregistered", Operator: Exists, Effect: NoExecute},
  {Operator: Exists, Key: "node.kubernetes.io/not-ready"},      // bootstrap window
]
RestartPolicy: Never
Container[0]:
  Image: nicolaka/netshoot@sha256:<digest-pinned-in-Phase-0>
  Command: /bin/sh -c 'sleep 86400'
  SecurityContext.Privileged: true
  Capabilities.Add: [SYS_ADMIN, NET_ADMIN, SYS_PTRACE]
  VolumeMounts:
    - {Name: containerd-sock, MountPath: <runtimeSocket>}    # supplied by NodeCache, not hardcoded
Volumes:
  - {Name: containerd-sock, HostPath: {Path: <runtimeSocket>, Type: Socket}}
Labels:
  auto.debugger/session:   <sessionID>
  auto.debugger/node-hash: <sha8(nodeName)>     # 63-char limit safe
  auto.debugger/ttl-epoch: <unix>
Annotations:
  auto.debugger/node:    <full nodeName>
Name: auto-debug-<sha8(node)>                   # deterministic; AlreadyExists → reuse
Namespace: <flag, default: auto-debug>          # auto-created on first run, not kube-system
```

Decisions vs. v1 pre-verify:
- **Drop hostPath `/`**. No host filesystem mount. Containerd socket is the only host mount.
- **Deterministic pod name** = `auto-debug-<sha8(node)>`. Solves the list-then-create race; `Create` on AlreadyExists path falls through to "fetch + reuse".
- **Tolerations** narrowed to the actual taint set seen on `i-0a449a5e52b88c278`.
- **Labels hashed** for 63-char safety; full name in annotation.
- **Namespace** defaults to dedicated `auto-debug`. `kube-system` allowed via `--namespace` for clusters with PSP / PSA blocking arbitrary namespace creation.

## Lifecycle State Machine

`Ensure(ctx, node) (podName string, terminated <-chan struct{}, err error)`:

1. Compute deterministic name `auto-debug-<sha8(node)>`.
2. `Get` pod by name.
3. If exists + `Running` + TTL annotation > now: refresh TTL annotation (`now+30m`); start watch on pod object; return name + termination channel.
4. If exists + Pending/Succeeded/Failed/Unknown OR TTL expired: `Delete` (foreground), wait gone, fall through.
5. If not exists: `Create` deterministic spec.
6. On `AlreadyExists` from Create: another command lost the race; goto step 2.
7. Watch pod, wait for `Running`. Deadline 60s. On timeout: dump pod events to stderr, return error code 2.
8. Start a goroutine watching the pod; close `terminated` channel when pod transitions out of `Running` so callers can abort streams.

Pod naming is deterministic, so concurrent `Ensure` calls converge on the same pod (no duplicate privileged pods per node). Mid-session multiplexing is safe because `kubectl exec` opens independent streams.

`Cleanup(ctx, filter)`:
- List by label selector `auto.debugger/session exists`.
- Optional `auto.debugger/node-hash=<sha8(X)>` narrowing.
- Delete with `GracePeriodSeconds=0`.

`SweepExpired(ctx)` runs at startup of every command:
- List label-selector pods, parse `auto.debugger/ttl-epoch` annotation, delete pods past TTL or whose Pod is `Succeeded`/`Failed`. Bounded to the active namespace.

### Sliding TTL semantics

TTL annotation is purely informational metadata; the pod runs `sleep 86400` (24h hard cap). Real GC paths:
- `SweepExpired` on next CLI invocation
- explicit `auto cleanup`
- v2: CronJob in target ns calling `auto cleanup --ttl-only`

## Exec Streaming

Use `k8s.io/client-go/tools/remotecommand` SPDY executor. `ExecStream(ctx, pod, container, argv, stdin, stdout, stderr)` returns when remote process exits.

### Namespace flag matrix (verified in `docs/VERIFY.md`)

| Use case | argv prefix | Why |
|---|---|---|
| Run host's `journalctl` / `systemctl` | `nsenter -t 1 -m -u -i --` | Need host mount-ns for systemd binaries + journals; net not required |
| Run pod's `curl` against host loopback | `nsenter -t 1 -n --` | Keeps netshoot mount-ns (curl present); host net-ns reaches `127.0.0.1:<port>` |
| Run pod's `tcpdump` on host net | `nsenter -t 1 -n --` | Same |
| Run pod's `tcpdump` in workload pod's net-ns | `nsenter -t <wpid> -n --` | wpid from `ctr` task list |
| Run host's `ctr` (PID resolution) | `nsenter -t 1 -m -u -i -- /usr/bin/ctr -n k8s.io tasks list` | Host has `ctr`, netshoot doesn't |
| `auto exec <node> -- <cmd>` (default) | `nsenter -t 1 -m -u -i -p --` | Most operator commands need host mount + pid |

**Anti-pattern**: `nsenter -t 1 -a` enters host mount-ns where Bottlerocket has no tcpdump/curl/jq/crictl. Banned.

**Anti-pattern**: wrapping host-side commands in `sh -c '...'`. Bottlerocket PID 1 is `brush` with restricted allowlist. Always pass argv directly.

### PID resolution (Blocker 2 fix — verified)

```go
func ResolveContainerPID(ctx, deps, node, containerID string) (int, error) {
    out, err := ExecCapture(ctx, deps, node,
        "nsenter", "-t", "1", "-m", "-u", "-i", "--",
        "/usr/bin/ctr", "-n", "k8s.io", "tasks", "list")
    // parse output:
    //   TASK                                                              PID    STATUS
    //   8b353b02d342067cb46d77...                                        2202   RUNNING
    for line in out.lines() {
        fields := strings.Fields(line)
        if fields[0] == containerID { return strconv.Atoi(fields[1]) }
    }
    return 0, ErrPIDNotFound
}
```

`containerID` extracted from pod status (`containerStatuses[i].containerID = "containerd://<sha>"`, prefix stripped).

### Multi-container handling

`ResolveContainerPID` requires exact containerID. For `auto observe tcpdump`:
- `--container NAME` flag selects when pod has >1 non-init container.
- Default: first non-init container, error if ambiguous.
- Init/ephemeral containers excluded unless `--init` / `--ephemeral` flags set.

## Per-Command Flow

### `observe tcpdump <pod> -n <ns> [--container N]`

1. Fetch target pod object → `nodeName`, containerID, list of containers (for `--container` validation).
2. `Ensure(nodeName)` → debug pod + termination channel.
3. `ResolveContainerPID(node, containerID)` via `ctr -n k8s.io tasks list`.
4. Pick effective duration: `--duration` flag (default `30s`); passed to remote **busybox** `timeout` (netshoot's `timeout` is busybox; GNU `--signal=` syntax does not exist).
5. Open local file `./tcpdump-<pod>-<ts>.pcap`. Open with `O_TRUNC` and `os.File.Sync()` after stream close.
6. Start a target-pod watcher: fetch target pod once, capture `pod.ResourceVersion`, then `clientset.CoreV1().Pods(ns).Watch` with `ResourceVersion = fetched.ResourceVersion`, filtered to the target pod name. This closes the fetch-vs-watch race (events between fetch and watch establishment are not lost). On `Deleted`, `Pending`, `Failed`, or `restartCount` increment, cancel root context.
7. Single ExecStream (raw pcap):
   ```
   nsenter -t <wpid> -n -- timeout -s INT -k 2 <dur> \
     tcpdump -i any -U -s <snaplen> -w - <filter>
   ```
   Stdout → local file. `tcpdump -U` writes the global pcap header up front and flushes per packet. **Verified live (round-2)**: SPDY-cancel mid-flight on `tcpdump -U -w -` produces a valid pcap file (5/5 trials). Round-1 codex concern about needing signal-forwarding wrapper is empirically not required for this flow. busybox `timeout` confirmed valid (3/3 trials).
8. Optional concurrent stderr summary stream (off by default to avoid double-capture):
   ```
   nsenter -t <wpid> -n -- timeout -s INT -k 2 <dur> tcpdump -i any -nn -l -c 200 <filter>
   ```
9. errgroup awaits both; pcap file `Sync()` + `Close()` regardless of success.
10. Termination channel from `Ensure` (debug pod death) and target-pod watcher (target death) both cancel ExecStream.

**Pcap-flush guarantee**: two layers — (1) remote `timeout` for duration, (2) SPDY-cancel on Ctrl-C / target-pod death / debug-pod death. Both validated to produce readable pcaps.

### `metrics <agent> <node>`

1. Resolve `<agent>` via `catalog.Lookup(agent)` → list of named endpoints. CLI flag `--endpoint NAME` (default `metrics`).
   - If `--endpoint` not given AND agent has no `metrics` endpoint (e.g. `pod-identity`): **error code 1**, message lists available endpoints (e.g. "agent 'pod-identity' has no metrics endpoint; try `--endpoint healthz`").
   - No silent fallback to a different endpoint.
2. Transport branch:
   - **kubelet metrics** → apiserver-proxy: `GET /api/v1/nodes/<node>/proxy/metrics`. No debug pod.
   - **kubelet healthz / cadvisor / metrics/resource** → apiserver-proxy with `/metrics/cadvisor` etc.
   - **everything else** → debug pod path: `Ensure(node)`, then ExecStream `nsenter -t 1 -n -- curl -sS -m 5 http://127.0.0.1:<port><path>`. Pod's mount-ns provides `curl`.
3. Loop body:
   - One fetch, sink to human (timestamped block) or json (one NDJSON record).
4. If `--tail`, sleep interval, goto loop. Else exit.

**Why apiserver-proxy is default for kubelet**: avoids in-pod bearer token plumbing; pod SA is `403` against `/metrics`; caller's RBAC (`nodes/proxy:get`) governs naturally.

### `logs <agent> <node>`

1. Resolve `<agent>` → unit name.
2. Probe (round-2 verified): `nsenter -t 1 -m -u -i -- /usr/bin/journalctl -u <unit> -n 1 --output=cat --no-pager`, capture stdout. If 0 bytes, fall back to filesystem find:
   ```
   nsenter -t 1 -m -u -i -- /usr/bin/find \
     /etc/systemd/system /<arch>-bottlerocket-linux-gnu/sys-root/usr/lib/systemd/system \
     -maxdepth 3 -name "<unit>.service"
   ```
   Both empty ⇒ unit absent. (Bottlerocket denies `systemctl status` even from root nsenter — `journalctl -n 0` exit code is `0` for both present & absent units, so it cannot discriminate.)
3. Arch detection: read once per session via `nsenter -t 1 -m -u -i -- /usr/bin/uname -m`; cache in `internal/kube/nodecache.go`.
4. Build journalctl args from flags: `-u <unit>` + `--since|-n|-f`. `--grep` regex is client-side filter (always); journalctl's server-side `--grep` not relied on across systemd versions.
5. ExecStream:
   ```
   nsenter -t 1 -m -u -i -- /usr/bin/journalctl -u <unit> --no-pager <flags>
   ```
   Stdout sink. For `--tail`/`-f`, the stream is unbounded; SIGINT from CLI cancels via context (acceptable here — journalctl exits cleanly on stream close).

### `exec <node> -- <cmd...>`

1. `Ensure(node)`.
2. ExecStream `nsenter -t 1 -m -u -i -p -- <cmd...>` with stdin/stdout/stderr wired to terminal. `--ns mount,uts,ipc,net,pid,cgroup` overrides flag set.
3. Exit code propagated. Bottlerocket `brush` shell gotcha: argv[0] is `<cmd[0]>` directly; no `/bin/sh -c` wrapping. Operators must pass full binary paths.

### `cleanup`

1. Build label selector from flags.
2. List pods → delete.

## Output

`internal/output.Sink` interface:

```go
type Sink interface {
    Record(map[string]any) error  // structured event
    Stream(io.Reader) error        // raw byte stream (pcap, journalctl tail)
    Close() error
}
```

Two impls: `human` (pretty), `json` (NDJSON to stdout). Selected once at `cmd init`.

## Container Runtime Endpoint Discovery (round-2 verified)

The containerd socket path is discovered, not hardcoded. Once per session, per node:

```go
// internal/kube/nodecache.go
func (n *NodeCache) ContainerRuntimeEndpoint(node string) (string, error) {
    if v, ok := n.cre[node]; ok { return v, nil }
    raw, err := n.cs.RESTClient().Get().AbsPath(
        "/api/v1/nodes/" + node + "/proxy/configz",
    ).DoRaw(ctx)
    var cfg struct{ Kubeletconfig struct{ ContainerRuntimeEndpoint string `json:"containerRuntimeEndpoint"` } `json:"kubeletconfig"` }
    json.Unmarshal(raw, &cfg)
    n.cre[node] = strings.TrimPrefix(cfg.Kubeletconfig.ContainerRuntimeEndpoint, "unix://")
    return n.cre[node], nil
}
```

Both EKS-Auto nodes verified to return `unix:///run/containerd/containerd.sock`. Pod spec sets `volumeMounts[0].mountPath = <discovered-path>`; if discovery fails, fall back to `/run/containerd/containerd.sock` and warn. RBAC: requires `nodes/proxy:get` on caller (already required for kubelet metrics).

## Namespace Bootstrap

`debugpod.EnsureNamespace(ctx, cs, ns, autoLabel bool)` — idempotent state machine:

1. **Get** namespace.
2. If absent: **Create** with label `pod-security.kubernetes.io/enforce=privileged` + annotations `auto.debugger/created-by=<caller>`, `auto.debugger/created-at=<ts>`. Return ok. RBAC: `namespaces:create`.
3. If present with required PSA label: return ok.
4. If present without required PSA label AND `autoLabel=true`: **Patch** label onto namespace. Return ok. RBAC: `namespaces:patch`.
5. If present without required PSA label AND `autoLabel=false`: **Refuse** — exit code 1 with message: `namespace <ns> exists but lacks 'pod-security.kubernetes.io/enforce=privileged'. Re-run with --auto-label to patch, or pre-create the namespace with the label.`

`auto install` is v1: `auto install [--namespace auto-debug] [--auto-label]` calls `EnsureNamespace(ns, autoLabel)`. PLAN Phase 0.5 ships it.

`Ensure(ctx, ns, node)` lazily calls `EnsureNamespace(ns)` on first invocation per session.

## Catalog (live-verified — see `docs/VERIFY.md`)

```go
type Endpoint struct {
    Name      string  // "metrics" | "healthz" | "ready" | "cadvisor" | ...
    Transport Transport
    Port      int     // for NodeLocalhost
    Path      string  // path including leading /
    Scheme    string  // http (default) | https
}

type Transport int
const (
    APIServerProxy Transport = iota   // GET /api/v1/nodes/<node>/proxy/<path>
    NodeLocalhost                     // nsenter -t 1 -n -- curl http://127.0.0.1:PORT/PATH
)

type Agent struct {
    Alias     string
    Unit      string
    Endpoints []Endpoint
    Notes     string
}

var Builtin = []Agent{
    {"kubelet", "kubelet.service", []Endpoint{
        {"metrics",  APIServerProxy, 0,    "/metrics",          ""},
        {"cadvisor", APIServerProxy, 0,    "/metrics/cadvisor", ""},
        {"resource", APIServerProxy, 0,    "/metrics/resource", ""},
        {"healthz",  NodeLocalhost,  10248,"/healthz",          "http"},
    }, "Kubernetes kubelet"},

    {"containerd", "containerd.service", nil, "Container runtime — no stable localhost endpoint"},

    {"kube-proxy", "kube-proxy.service", []Endpoint{
        {"metrics", NodeLocalhost, 10249, "/metrics", "http"},
        {"healthz", NodeLocalhost, 10256, "/healthz", "http"},
    }, "Kubernetes kube-proxy"},

    {"ipamd", "ipamd.service", []Endpoint{
        {"metrics",     NodeLocalhost, 61678, "/metrics", "http"},
        {"introspect",  NodeLocalhost, 61679, "/v1/enis", "http"},
    }, "AWS VPC CNI IPAMD"},

    {"network-policy", "aws-network-policy-agent.service", []Endpoint{
        {"metrics", NodeLocalhost, 8900, "/metrics", "http"},
        {"healthz", NodeLocalhost, 8901, "/healthz", "http"},
    }, "AWS Network Policy Agent"},

    {"node-monitor", "eks-node-monitoring-agent.service", []Endpoint{
        {"healthz", NodeLocalhost, 8800, "/healthz", "http"},
        {"metrics", NodeLocalhost, 8801, "/metrics", "http"},
    }, "EKS Node Monitoring Agent (Auto)"},

    {"pod-identity", "eks-pod-identity-agent.service", []Endpoint{
        {"healthz", NodeLocalhost, 2703, "/healthz", "http"},
    }, "EKS Pod Identity Agent — no metrics endpoint"},

    {"coredns", "coredns.service", []Endpoint{
        {"metrics", NodeLocalhost, 9153, "/metrics", "http"},
        {"health",  NodeLocalhost, 8080, "/health",  "http"},
        {"ready",   NodeLocalhost, 8181, "/ready",   "http"},
    }, "CoreDNS"},

    {"healthchecker", "eks-healthchecker.service", nil, "EKS Auto health checker — no exposed endpoint"},
    {"ebs-csi",       "eks-ebs-csi-driver.service", nil, "EBS CSI node driver"},
    {"ebs-csi-registrar", "eks-ebs-csi-driver-registrar.service", nil, "EBS CSI Driver registrar"},
    {"efa",           "efa-k8s-device-plugin.service", nil, "EFA device plugin"},
}
```

`Lookup(alias)` falls back to `{Unit: alias, Endpoints: nil}` if not found, allowing arbitrary unit names with mandatory `--port`/`--path`.

## Error Model

```
Code  Meaning
0     ok
1     user error (bad flag, missing arg)
2     cluster error (apiserver, RBAC, pod scheduling)
3     node error (nsenter failed, unit absent, command non-zero)
130   SIGINT
```

Errors written as one line on stderr, structured under `--json`:

```
{"level":"error","code":"node","msg":"unit not present","node":"i-...","unit":"foo.service"}
```

## Concurrency / Cancellation

- `cmd/auto/main.go` installs a SIGINT handler that cancels the root context.
- ExecStream cancellation closes the SPDY connection but does **not** reliably deliver SIGINT to the remote process. We don't depend on it.
- For long-running remote commands that must terminate cleanly, wrap the command with **busybox `timeout`** (netshoot's `timeout` is busybox, syntax `timeout -s INT -k <kill-after-secs> <dur-secs> <cmd>`; GNU `--signal=` and `--kill-after=` flags do NOT work — verified in `VERIFY.md`).
- tcpdump: remote `timeout -s INT -k 2 <dur> tcpdump ...` is the duration source of truth. errgroup awaits raw+summary streams; first error cancels both.
- `--duration 0` for tcpdump: NO `timeout` wrapper. Stream runs until SPDY-cancel (Ctrl-C, target-pod death, debug-pod death). Implementation MUST branch on duration=0 to omit the busybox timeout prefix.
- journalctl `-f` and `metrics --tail`: SPDY cancel is sufficient — both react to stdin/stdout EOF on next loop.
- `Ensure` returns a termination channel closed when the debug pod stops being `Running`; commands select on this channel and abort gracefully.

## Security Considerations

- **Privileged hostPID pod = node-root, effectively cluster-admin equivalent**. Anyone able to `create` `Pod` with `privileged: true, hostPID: true` in any namespace can escalate to node root and read every secret on that node. SECURITY.md must call this out at the top.
- **RBAC reality**: caller needs `pods:create` (with privileged Pod admission allowed). On clusters with PSA `restricted` enforced cluster-wide, `auto` requires a namespace labeled `pod-security.kubernetes.io/enforce=privileged`. The default namespace `auto-debug` is created with that label by `auto install` (not yet implemented; until then user must pre-create with the label).
- **Image pinning**: `nicolaka/netshoot@sha256:<digest>` pinned in `cmd/auto/main.go`. Phase 0 records the digest used for v0.1.0.
- **Node guardrails**:
  - `auto.debugger/protect=true` node label blocks all `auto` operations on that node (CLI-side check).
  - `--require-cluster-suffix` / `--require-context-prefix` flags configurable in `~/.config/auto/config.yaml`; refuses to operate on contexts not matching the allowlist. Default: warn-only.
  - Production cluster names containing `-prod-` / `-prd-` print a confirmation prompt unless `--yes`.
- **No host filesystem access by default**. The only host mount is the containerd socket (UNIX socket, not a directory). All journaling/file-reading happens via `nsenter -t 1 -m` against host bins; no writable bind-mounts.
- **Audit trail**: every command emits an `auto.debugger/audit` annotation onto the debug pod with caller user (from `kubectl whoami`-equivalent SubjectAccessReview), command, and timestamp.

## Testing

- Unit: `nsenter` and `catalog` packages — pure, table-driven.
- Integration (live): the verify script in `docs/VERIFY.md` against the `auto-test` cluster. No mocked apiserver in v1.

## v2 Backlog

- Custom hardened image, digest-pinned.
- `auto fetch` (file copy off node).
- Persistent DaemonSet via `auto install`.
- S3 upload for long pcap.
- ServiceAccount auth path.
- Auto-cleanup CronJob.
- Multi-node fan-out (`auto logs kubelet --all-nodes`).
