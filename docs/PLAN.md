# Auto Debugger — Implementation Plan

## Phase 0 — Repo init + image pin (20 min)
- `go mod init github.com/viveksb007/autoscope`
- Add deps: `cobra`, `client-go`, `cli-runtime`, `kubectl/pkg/cmd/exec`.
- Stub `cmd/auto/main.go` with cobra root + version subcommand.
- Pin `nicolaka/netshoot` digest: `crane digest nicolaka/netshoot:latest` → record in `internal/debugpod/manifest.go`.
- Verify `go build ./cmd/auto` produces `./auto`.

## Phase 0.5 — TOOL.md + COMPLIANCE.md + bootstrap (45 min)
- Author `docs/TOOL.md` per-command schema (synopsis, args, flags, output schema, exit codes, examples) for every command in REQUIREMENTS.md, BEFORE writing implementation. Used as acceptance test fixture in Phase 6. **Canonical source of CLI flag precedence and fallback behavior** — REQS/DESIGN reference TOOL.md.
- Author `docs/COMPLIANCE.md` with 8–12 canned operator/agent prompts and the expected `auto ...` invocation sequence each should produce.
- Implement `auto install` (`internal/cli/install.go` + `internal/debugpod/namespace.go`) — idempotent namespace creation with PSA `privileged` label.

## Phase 1 — Core debugpod layer (1.5–2h)
Files: `internal/kube/client.go`, `internal/debugpod/{manifest,lifecycle,exec}.go`.

- `kube.Client(kubeconfigPath, contextName) (*kubernetes.Clientset, *rest.Config, error)`.
- `kube.NodeCache.ContainerRuntimeEndpoint(node string) (string, error)` — apiserver-proxy `configz` lookup, cached. Falls back to `/run/containerd/containerd.sock` with warning on configz failure.
- `debugpod.buildPodSpec(node, ns, imageDigest, runtimeSocket string) *corev1.Pod` — deterministic name, hashed labels, narrow tolerations, runtime-socket hostPath mount (path supplied by NodeCache).
- `debugpod.Ensure(ctx, cs, ns, node) (podName string, terminated <-chan struct{}, err error)`:
  - Get-then-create-then-AlreadyExists-recover pattern.
  - Wait Running w/ deadline.
  - Refresh TTL annotation.
  - Return termination channel from pod watch.
- `debugpod.SweepExpired(ctx, cs, ns) error` — opportunistic GC at every command start.
- `debugpod.Cleanup(ctx, cs, ns, filter) error` — label-selector delete.
- `debugpod.ExecStream(ctx, cfg, ns, pod, container, argv, stdin, stdout, stderr) error` — wraps remotecommand SPDY. Note: cancel does NOT propagate signals; commands wrap with `timeout(1)` where flush-on-deadline matters.

Smoke test inline: `auto exec` against test cluster.

## Phase 2 — nsenter helpers + catalog (45 min)
Files: `internal/nsenter/*.go`, `internal/catalog/agents.go`.

- `nsenter.HostMount(cmd ...string) []string` → `["nsenter","-t","1","-m","-u","-i","--", cmd...]` for journalctl/ctr.
- `nsenter.HostNet(cmd ...string) []string`   → `["nsenter","-t","1","-n","--", cmd...]` for curl/tcpdump-host-net (uses pod's mount-ns).
- `nsenter.PodNet(pid int, cmd ...string) []string` → `["nsenter","-t",strconv.Itoa(pid),"-n","--", cmd...]`.
- `nsenter.HostExec(nsSet []string, cmd ...string) []string` — generic for `auto exec`.
- `nsenter.JournalCtl(unit string, opts JournalOpts) []string` — wraps with HostMount + `/usr/bin/journalctl`.
- `nsenter.CtrTaskList() []string` — wraps with HostMount + `/usr/bin/ctr -n k8s.io tasks list`.
- `nsenter.Curl(url string, timeout time.Duration) []string` — wraps with HostNet + netshoot's curl.
- Removed: `SystemctlStatus` — Bottlerocket denies it; use journalctl probe instead.
- Removed: `CrictlInspectPID` — replaced by parsing `ctr` task list.
- `catalog.Lookup(alias string) Agent` with fallback to `{Unit: alias}`. Catalog populated from `docs/VERIFY.md` results.

## Phase 3 — Cobra commands (1.5h)
Files: `internal/cli/{root,observe_tcpdump,metrics,logs,exec,cleanup,version}.go`.

- `root.go` — global flags `--kubeconfig`, `--context`, `--namespace`, `--json`, `-v`. Builds shared deps (clientset, sink) once.
- Each subcommand keeps to flag parsing + calling `debugpod.ExecStream`.
- Output through `internal/output` sink.

## Phase 4 — tcpdump orchestration (1h)
Files: `internal/tcpdump/capture.go`, `internal/kube/podwatch.go`.

- `Capture(ctx, deps, target tcpdump.Target, opts tcpdump.Opts) error`:
  1. Resolve target pod → nodeName, list of containers, selected container (`--container` flag, default first non-init, error if ambiguous).
  2. `Ensure(node)` debug pod.
  3. Resolve workload PID via `nsenter -t 1 -m -u -i -- /usr/bin/ctr -n k8s.io tasks list` parsed in Go (NOT crictl — Bottlerocket lacks crictl, see VERIFY.md).
  4. Start target-pod watcher; cancels root ctx on `Deleted`/`Pending`/`Failed`/restart.
  5. errgroup of one (or two with `--summary`) ExecStream calls, each wrapped with busybox `timeout -s INT -k 2 <dur>`.
  6. Local file writer with `O_TRUNC` open and `Sync()` + `Close()` in defer.
  7. Cancel on duration (remote timeout), Ctrl-C (SPDY-cancel — verified valid pcap), debug-pod death, target-pod death.

## Phase 5 — Docs polish (30 min)
- `docs/TOOL.md` — already drafted in Phase 0.5; reconcile any drift from implementation.
- `docs/README.md` — human user guide.
- `docs/SECURITY.md` — blast radius, RBAC, protect-label, PSA requirement, image-digest provenance.
- `docs/VERIFY.md` — live verify transcript (already populated).
- `docs/COMPLIANCE.md` — already drafted in Phase 0.5.

## Phase 6 — Live verify (45 min, against `auto-test` cluster)

**Pre-blocker verification already done in `docs/VERIFY.md`** — namespace flags, ctr-PID resolution, kubelet apiserver-proxy, agent catalog all confirmed. Phase 6 now exercises the binary itself end-to-end against `i-0a449a5e52b88c278`:

1. `./auto exec i-0a449a5e52b88c278 -- /usr/bin/journalctl -u kubelet -n 5 --no-pager` → 5 kubelet log lines.
2. `./auto logs kubelet i-0a449a5e52b88c278 --lines 20` → journalctl path.
3. `./auto metrics kubelet i-0a449a5e52b88c278` → apiserver-proxy returns `kubelet_*` series.
4. `./auto metrics ipamd i-0a449a5e52b88c278` → node-localhost path through debug pod returns IPAMD metrics.
5. `./auto metrics kubelet i-0a449a5e52b88c278 --endpoint healthz` → `ok`.
6. `kubectl run nginx --image=nginx -n default`, then `./auto observe tcpdump nginx -n default --filter "port 80" --duration 10s` while `kubectl exec nginx -n default -- curl -s http://example.com` runs in background → pcap opens in Wireshark.
7. Concurrent reuse: run `./auto logs kubelet ...` and `./auto metrics kubelet ...` in parallel shells → confirm one debug pod, two streams, no AlreadyExists errors.
8. `./auto cleanup --all --yes` → confirm pods removed.
9. Compliance test: open fresh Claude session, paste `docs/TOOL.md`, run prompts from `docs/COMPLIANCE.md`, score expected vs. produced commands.
10. Append transcripts to `docs/VERIFY.md`.

## Phase 7 — Polish
- Goreleaser config for cross-platform builds.
- README badges.
- Tag `v0.1.0`.

## Risks

| Risk | Mitigation |
|---|---|
| netshoot image pull blocked on private nodes | Document `--image` override; v1.1 ship hardened image |
| Bottlerocket `brush` rejects shell wrappers | argv passed directly, never `sh -c`; documented in TOOL.md |
| `systemctl status` denied | Use `journalctl -u <u> -n 1 --output=cat` stdout-byte-count as liveness probe (exit code is always 0 even for absent units; verified in VERIFY.md round-2) |
| Auto monitor unit names drift across releases | Catalog fallback to user-supplied unit name; CI re-runs VERIFY.md against newest Auto AMI |
| tcpdump in pod netns drops packets at high rate | Document snaplen + `-B` buffer flag escape hatch |
| Caller lacks RBAC | Pre-flight `SelfSubjectAccessReview` on each verb at startup |
| PSA `restricted` blocks privileged pod creation | `auto install` creates `auto-debug` ns labeled `pod-security.kubernetes.io/enforce=privileged`; document fallback |
| Race: two CLIs Ensure same node | Deterministic pod name + AlreadyExists retry — no duplicate pods |
| SPDY cancel doesn't flush tcpdump | Wrap remote `tcpdump` with `timeout(1)` — flushes before exit |
| Concurrent commands on same pod | Each ExecStream is independent; multi-stream tested in Phase 6 step 7 |
| Multi-arch sys-root path | Only matters for unit-file inspection in catalog discovery; CLI uses unit alias, not raw path — no impact |

## Estimate

~6h heads-down to ship working v1 + docs + live verify (added 1h for Phase 0.5 TOOL.md-first authoring + COMPLIANCE eval).
