# CLAUDE.md

Guidance for Claude (or any AI coding agent) working in this repo.

## What this project is

`auto` — Go CLI that creates an on-demand privileged hostPID pod on an EKS Auto Mode (Bottlerocket) node and exposes `tcpdump`, `journalctl`, on-host Prometheus metrics, and arbitrary `nsenter -t 1` commands behind a small subcommand surface.

Replaces the SSH-and-poke workflow operators previously hand-rolled when debugging Auto nodes (which have no SSH, no user-accessible SSM session).

## Source-of-truth files (read these first)

| File | Purpose |
|---|---|
| `docs/TOOL.md` | **Canonical CLI contract**. Flag precedence, exit codes, output schemas. If code disagrees with TOOL.md, update one of them — don't fork. |
| `docs/REQUIREMENTS.md` | FR/NFR + acceptance criteria. WHY behind features. |
| `docs/DESIGN.md` | Architecture, lifecycle state machines, per-command flow. HOW it's built. |
| `docs/VERIFY.md` | **Live-verified facts** about Bottlerocket EKS Auto. Never assume; check this first when behavior is in question. |
| `docs/SECURITY.md` | Blast radius, RBAC table, operator checklist. |
| `docs/COMPLIANCE.md` | 12 prompts that test whether an LLM agent can drive `auto` from TOOL.md alone. |
| `docs/PLAN.md` | Phased delivery plan. Mostly historical now. |
| `docs/REVIEW-codex-{01,02,03}.md` | Adversarial review history. Useful when unsure why a design choice exists. |

## Bottlerocket / EKS Auto facts that bite (live-verified)

These are NOT obvious from documentation. They were discovered the hard way and fixing the wrong assumption is a multi-hour debug. Treat as constraints, not optional knowledge.

1. **`nsenter -t 1 -a` is banned.** Bottlerocket host has no `tcpdump`, `curl`, `jq`, `crictl`. Entering host mount-ns loses every netshoot binary. Use the namespace-flag matrix:

   | Need | nsenter flags |
   |---|---|
   | journalctl, ctr, host bins | `-t 1 -m -u -i` (host mount + uts + ipc, NOT net) |
   | curl loopback / tcpdump host net | `-t 1 -n` (keeps pod's mount-ns ⇒ netshoot bins reachable) |
   | tcpdump in workload pod's net-ns | `-t <wpid> -n` |

2. **PID 1 shell is `brush`** with restricted allowlist. `which`, `command -v`, `ls`, etc. denied. Always pass full binary paths (`/usr/bin/journalctl`). Never wrap argv in `sh -c`.

3. **`systemctl status / list-units / is-active` denied** even from privileged hostPID nsenter (Bottlerocket SELinux). For unit-existence probe use `journalctl -u <unit> -n 1 --output=cat` byte-count, fallback to `find` against `/etc/systemd/system` + arch-suffixed sys-root path.

4. **`crictl` not on host. `ctr` is.** PID resolution: `nsenter -t 1 -m -u -i -- /usr/bin/ctr -n k8s.io tasks list`, parse 2nd column.

5. **kubelet `:10248` is healthz only**, NOT metrics. Real metrics:
   - Default: apiserver-proxy `/api/v1/nodes/<n>/proxy/metrics` (caller's RBAC, no in-pod token plumbing).
   - In-pod SA can't auth to `:10250/metrics` (returns 403).

6. **netshoot `timeout` is busybox**, not GNU. Syntax: `timeout -s INT -k <kill-secs> <dur-secs> <cmd>`. `--signal=` and `--kill-after=` flags don't exist.

7. **SPDY exec stream cancel does NOT deliver SIGINT to remote process.** For deadline-bounded commands wrap with busybox `timeout`. For Ctrl-C: tcpdump `-U` produces valid pcap on SPDY-cancel (verified 5/5 trials), but the remote process keeps running until apiserver exec stream timeout (~10min) — so don't `wait` indefinitely on the auto pid.

8. **Container runtime endpoint is discovered, not hardcoded.** `kubectl get --raw /api/v1/nodes/<n>/proxy/configz` → `.kubeletconfig.containerRuntimeEndpoint`. Cached per-node in `internal/kube/nodecache.go`.

9. **AWS Network Policy Agent + IPAMD write structured logs to `/var/log/aws-routed-eni/*.log`**, NOT just journald. ANPA enforcement events (eBPF map programs, PolicyEndpoint reconciles) go to `network-policy-agent.log`, not `journalctl -u aws-network-policy-agent`. Catalog has `LogSource[]` per agent — index 0 is the default.

## Code layout

```
cmd/auto/main.go              entry: signal handler + cobra
cmd/smoke/main.go             dev throwaway harness for Phase 1 verify
internal/cli/                 cobra commands; one file per subcommand + completion
internal/debugpod/            Ensure/Cleanup/ExecStream/Manifest — only layer that touches kube
internal/nsenter/             argv builders. PURE: no kube, no IO. Unit tests live here.
internal/catalog/             12-agent live-verified catalog (Endpoints + Logs)
internal/kube/                clientset loader + NodeCache (configz lookup)
internal/exitcode/            stable exit-code contract from docs/TOOL.md
internal/tcpdump/             pcap orchestration: target-pod watcher + errgroup
test/                         e2e bash harness, 38 assertions, ~45s runtime
docs/                         see source-of-truth table
```

Layer rules (do NOT violate):
- `nsenter` and `catalog` are pure. No kube imports.
- Only `debugpod` and `kube` touch the K8s API.
- `cli/*` reads ctx → calls debugpod → emits stdout/stderr. No business logic in cli.
- exitcode.Wrap at the boundary (cli or debugpod), never in pure layers.

## Conventions

- **Caller's RBAC, not a ServiceAccount.** Caller's kubeconfig drives every API call.
- **Deterministic pod name** `auto-debug-<sha8(node)>`. AlreadyExists → reuse, never create duplicate.
- **Image pinned** via `--image @sha256:...`. `:latest` default is a known TODO (see `internal/cli/root.go` and SECURITY.md).
- **Output**: human text default. `--json` flag → NDJSON. Streaming commands emit one record per event.
- **Exit codes**: 0 ok, 1 user, 2 cluster, 3 node, 4 internal, 130 sigint. Defined in `internal/exitcode`.
- **No hostPath fs mount** other than the discovered containerd socket. Everything else goes through nsenter.
- **No SAR preflight** in v0.1 (deferred). Surface RBAC errors at first kube-API call.

## Development flow

This project was developed with adversarial review at each phase. Pattern:

1. Write code for one phase.
2. Build + smoke test against live cluster (`auto-test` EKS Auto, two-node).
3. Spawn `cavecrew-reviewer` subagent against the diff. Address every finding.
4. Re-run reviewer until reply is `CLEAN`.
5. Commit with phase tag (`phase 0:`, `phase 1:`, etc).

Each phase took ~1-2h. Full MVP shipped in 7 phases. See `docs/PLAN.md` and the commit log.

For docs, the same pattern with `codex` instead of cavecrew. Three review rounds against pre-impl docs caught 17 blockers/highs before we wrote any Go code.

## Live testing

Always test against a live EKS Auto cluster. Do NOT trust mocked apiserver behavior — Bottlerocket gotchas only surface live.

```sh
make e2e KCTX=<your-context> TARGET_NODE=<i-...>
```

38 assertions across 8 sections (install, exec, logs, metrics, observe, cleanup, completion, guardrails). Runs in ~45s. Self-cleanup via trap.

E2E catches things go-test cannot:
- SPDY exec timing
- busybox vs GNU `timeout` syntax
- nsenter ns flag interactions on real Bottlerocket
- TTL race recovery in lifecycle.go

When adding a new flag or command, add a corresponding assertion in `test/lib/0XX_*.sh`.

## Things that look wrong but aren't

- `cmd/smoke/main.go` exists. It's a dev harness from Phase 1 verify. Keep or delete; not part of the shipped CLI.
- `internal/tcpdump/capture.go` has a `fanDone` channel + 2 nested defers. That's the synchronization fix from Phase 4 review — don't simplify it.
- Catalog has `Logs []LogSource` ordered list, index 0 = default. Order matters for ANPA + IPAMD; do NOT alphabetize.
- `auto observe tcpdump --duration 0` does NOT wrap remote tcpdump in `timeout`. Documented choice (see DESIGN.md). Ctrl-C still produces a valid pcap (verified) but the remote process may run until apiserver stream timeout.
- `auto exec` deliberately doesn't `sh -c` wrap argv. Bottlerocket brush rejects most utilities; full binary paths are required.

## Adding a new subcommand checklist

1. Update `docs/TOOL.md` first (canonical contract).
2. Add `internal/cli/<name>.go`. Wire into `internal/cli/root.go`.
3. If it touches a host binary not yet used, verify against live cluster first (`auto exec <node> -- /usr/bin/<bin> --version`).
4. Add 3-6 assertions in `test/lib/0XX_<name>.sh`. Cover happy path + at least one error path.
5. Run `make e2e KCTX=... TARGET_NODE=...`. All sections green.
6. Spawn `cavecrew-reviewer` over the diff; fix findings.
7. Commit with `<verb>: <one-line>` subject.

## Adding a new agent to the catalog

1. **Live-verify first.** SSH/exec into the cluster + confirm:
   - Unit name (check `/etc/systemd/system/multi-user.target.wants/`)
   - Listening ports (`ss -tlnp`)
   - Endpoints respond (`curl http://127.0.0.1:<port>/<path>`)
   - File log paths if applicable (`ls /var/log/aws-routed-eni/`)
2. Append to `internal/catalog/agents.go:Builtin`. **Order Logs by usefulness** — index 0 is the default for `auto logs <agent>`.
3. Update `docs/TOOL.md` Catalog tables (endpoints + log sources).
4. Update `docs/VERIFY.md` if you discovered new facts.
5. Add a test in `test/lib/030_logs.sh` or `040_metrics.sh`.

## Anti-patterns to refuse

- Adding a hostPath mount of `/` or `/var/log`. The pod has containerd-sock only; everything else via nsenter.
- Wrapping host commands in `sh -c '...'`. Bottlerocket brush rejects it.
- Calling `crictl`. It's not on Bottlerocket. Use `ctr`.
- `nsenter -t 1 -a`. Banned. See gotcha #1.
- Using `--port` without `--path` (or vice versa) in `auto metrics`. Both must be set together; resolveEndpoint enforces this with exit code 1.
- Trusting `--latest` image tags. Pin digests.
- Adding RBAC verbs to docs/SECURITY.md or docs/REQUIREMENTS.md without grep'ing the code first. Drift between docs and code is the #1 review finding category.

## Useful commands during development

```sh
# build
go build -o auto ./cmd/auto

# unit tests (only nsenter + catalog have them; rest is e2e-tested)
go test ./...

# build + run e2e
make e2e KCTX=<ctx> TARGET_NODE=<node>

# tail ANPA enforcement (file log, not journal)
./auto logs network-policy <node> --tail

# follow eBPF SDK ops
./auto logs network-policy <node> --source bpf -f

# kubelet metrics (apiserver-proxy)
./auto metrics kubelet <node>

# pcap from a pod's net-ns w/ filter + duration cap
./auto observe tcpdump <pod> -n <ns> --filter "port 80" --duration 30s

# raw escape hatch (full binary paths required)
./auto exec <node> -- /usr/bin/ctr -n k8s.io containers list

# cleanup
./auto cleanup --all --yes
```

## What's not yet done

- Image digest pinning (TODO in `internal/cli/root.go`).
- `auto fetch <node>:<path>` — copy file off node. Needed for long pcap captures that live on host.
- `auto top` / `auto netstat` / `auto conntrack` named wrappers — currently require `auto exec`.
- Persistent DaemonSet mode (`auto install --daemonset`) for sub-second command latency.
- `auto.debugger/protect=true` node label refusal — documented but not enforced.

If you add any of these, update TOOL.md + COMPLIANCE.md prompts + SECURITY.md if RBAC changes.
