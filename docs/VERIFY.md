# Live Verification Transcript — Blockers + Catalog

Date: 2026-06-04
Cluster: `auto-test.us-west-2.eksctl.io`
Node: `i-0a449a5e52b88c278` (Bottlerocket EKS Auto, Standard 2026.6.3, kernel 6.12.88, arm64)
Image: `nicolaka/netshoot:latest`

> **Note**: the manifest used in this transcript differs from the production manifest in `DESIGN.md`. Verify-only deltas:
> - namespace `kube-system` (production: `auto-debug`)
> - image tag `:latest` (production: `@sha256:<digest>` pinned in Phase 0)
> - tolerations `{operator: Exists}` (matches production — tolerate-everything; see DESIGN.md rationale)
> - extra read-only `/host/proc` and `/host/var/log` mounts (production: only containerd socket)
>
> These deltas do not affect any of the test results — every probe runs against host PID 1 / pod-net / host-net, none touch the diff above.

## Pod Manifest Used

```yaml
apiVersion: v1
kind: Pod
metadata: { name: auto-debug-verify, namespace: kube-system }
spec:
  nodeName: i-0a449a5e52b88c278
  hostPID: true
  hostNetwork: true
  hostIPC: true
  restartPolicy: Never
  tolerations: [{operator: Exists}]
  containers:
  - name: debug
    image: nicolaka/netshoot:latest
    command: ["/bin/sh","-c","sleep 3600"]
    securityContext:
      privileged: true
      capabilities: { add: [SYS_ADMIN, NET_ADMIN, SYS_PTRACE] }
    volumeMounts:
    - { name: containerd-sock, mountPath: /run/containerd/containerd.sock }
  volumes:
  - { name: containerd-sock, hostPath: { path: /run/containerd/containerd.sock, type: Socket } }
```

Pod Ready in ~10s.

## Bottlerocket Surprises

1. Host PID 1 shell is `brush` — restricted allowlist. `which`, `command -v`, `ls`, etc. denied. Always pass full binary paths (`/usr/bin/journalctl` etc.) when invoking host-side via nsenter, OR `nsenter -t 1 -m -u -i -- <bin>` directly (no shell wrapper).
2. `systemctl list-units` / `is-active` / `is-failed` / `status` all return **Access denied**. Root-via-nsenter doesn't bypass Bottlerocket SELinux. Use `journalctl -u <unit>` for log-based liveness or probe known localhost ports.
3. Host has no `crictl`. Host has `ctr` at `/usr/bin/ctr`. netshoot has neither out of box.
4. Unit files live under `/$ARCH-bottlerocket-linux-gnu/sys-root/usr/lib/systemd/system/` symlinked from `/etc/systemd/system/<target>.wants/<unit>.service`.
5. ExecStart paths are split across base unit + `*.service.d/exec-start.conf` drop-ins; base unit often shows `/usr/bin/false` placeholder.

## Blocker 1 — nsenter namespace split

**Hypothesis**: keep mount-ns inside debug pod (netshoot) for tools; enter only host net-ns or pid+mount-ns selectively.

| Probe | Command | Result |
|---|---|---|
| netshoot has tcpdump/curl/jq | `command -v tcpdump curl jq` | ✅ all present |
| Host bin paths | `nsenter -t 1 -m -u -i -- journalctl --version` | ✅ systemd 257 |
| Host has tcpdump/curl/jq | n/a | ❌ host has none — confirmed `nsenter -t 1 -a -- which tcpdump` fails |
| curl host-localhost via `-t 1 -n` | `nsenter -t 1 -n -- curl http://localhost:10248/healthz` | ✅ `200` |
| journalctl via `-t 1 -m -u -i` | `nsenter -t 1 -m -u -i -- journalctl -u kubelet -n 1` | ✅ logs streamed |

**Fix confirmed**:
- `journalctl`/`systemctl status` (where allowed) → `nsenter -t 1 -m -u -i -- <bin>`
- `curl`/`tcpdump --host-net`/etc. → `nsenter -t 1 -n -- <bin>` (uses pod's mount ns ⇒ netshoot binaries)
- `tcpdump --pod-net` → `nsenter -t <pid> -n -- tcpdump ...`

## Blocker 2 — crictl/PID resolution

**Hypothesis**: mount containerd socket; use `crictl --runtime-endpoint`.

**Result**: netshoot lacks crictl. Two viable paths:

A. **Use host `ctr -n k8s.io tasks list`** — works as-is. Output:
```
TASK                                                                PID     STATUS
8b353b02d342067cb46d77dd4f5fb0d83262c2f748e67a3ebc7bc0548c24c88c    2202    RUNNING
```
ContainerID from pod status (`containerStatuses[].containerID = "containerd://<sha>"`) → strip prefix → grep against `ctr` output.

B. **Bake crictl into custom image** — v1.1.

**Decision (v1)**: path A. Socket mount still needed for any pod-side tooling later. PID resolution shell:
```bash
nsenter -t 1 -m -u -i -- /usr/bin/ctr -n k8s.io tasks list \
  | awk -v c="$CID" '$1==c {print $2}'
```

tcpdump end-to-end (target = `metrics-server` pod, container PID 2202):
```
nsenter -t 2202 -n -- tcpdump -i any -c 5 -nn
# captured 5 packets, 0 dropped, valid pcap on stdout-> file (1977 bytes, "tcpdump capture file v2.4")
```

✅ Pcap path validated.

## Blocker 3 — kubelet metrics endpoint

| Endpoint | Method | Result |
|---|---|---|
| `http://localhost:10248/healthz` | direct curl | ✅ `200` (healthz only — not metrics) |
| `https://localhost:10250/metrics` (no auth) | direct curl | ❌ `401` |
| `https://localhost:10250/metrics` (in-pod SA token) | bearer header | ❌ `403` (pod SA lacks `nodes/metrics`) |
| `kubectl get --raw /api/v1/nodes/<node>/proxy/metrics` | apiserver-proxy | ✅ `200`, **704 `kubelet_*` series** |

**Fix confirmed**: default kubelet metrics path goes via apiserver-proxy. No pod-side bearer token needed; caller's RBAC governs (`nodes/proxy` get).

## Bonus — Auto Agent Catalog (live-confirmed)

Listening localhost ports observed via `ss -tlnp`:

| Process | PID | Endpoints (verified 200) |
|---|---|---|
| kubelet | 1717 | `:10248/healthz`, `:10250/metrics` (apiserver-proxy) |
| kube-proxy | 1645 | `:10249/metrics`, `:10256/healthz` |
| ipamd (AWS VPC CNI) | 1843 | `:61678/metrics`, `:61679/v1/enis` |
| aws-network-policy-agent | 1960 | `:8900/metrics`, `:8901/healthz` |
| eks-node-monitoring-agent | 1599 | `:8800/healthz`, `:8801/metrics` |
| eks-pod-identity-agent | 1690 | `:2703/healthz` (no metrics endpoint exposed) |
| coredns | 2052 | `:9153/metrics`, `:8080/health`, `:8181/ready` |
| containerd | 1610 | `:39673` (debug, ephemeral; not stable) |

Unit names (verified via `journalctl -u <u> -n 1`):
`kubelet`, `containerd`, `kube-proxy`, `ipamd`, `aws-network-policy-agent`,
`eks-node-monitoring-agent`, `eks-pod-identity-agent`, `eks-healthchecker`,
`eks-ebs-csi-driver`, `eks-ebs-csi-driver-registrar`, `efa-k8s-device-plugin`,
`coredns`, `coredns-bootstrap`, `configure-snapshotter`, `warm-pool-wait`,
`systemd-networkd`.

## Implications for Design Rev2

- **Catalog** populated with concrete `(unit, port, path)` for all agents above. Drop "TBD" rows.
- **Default kubelet metrics** path = apiserver-proxy. Add localhost-curl mode behind `--via=node` flag for unit catalog entries that aren't kubelet.
- **`auto exec`** must run direct binaries with full paths (no `sh -c` wrapping when target is host PID 1) since `brush` rejects most utilities.
- **`auto logs`** uses `nsenter -t 1 -m -u -i -- journalctl -u <unit> ...`. `systemctl`-based liveness/status NOT available — design must skip the `systemctl cat` probe; instead probe with `journalctl -u <u> -n 0 --since 1m`.
- **PID resolution for tcpdump** uses `ctr -n k8s.io tasks list` filtered by containerID. No jq dep.
- **containerd socket hostPath mount** kept (cheap; useful when v1.1 adds custom image with crictl).
- **netshoot:latest** acceptable for v1; image-pin to digest in Phase 0 of impl.
- **Multi-arch**: test node was arm64; confirm amd64 path `/x86_64-bottlerocket-linux-gnu/sys-root/...` exists by reading `uname -m` once per node session.

## Cleanup

```
kubectl delete pod auto-debug-verify -n kube-system --grace-period=0 --force
```

## Round-2 verifications (rev3 inputs)

### Liveness probe (replaces broken `journalctl -n 0` design)

| Probe | Present unit (`kubelet`) | Absent unit (`fake-not-real`) | Verdict |
|---|---|---|---|
| `journalctl -u <u> -n 1 --output=cat --no-pager` stdout bytes | `364` | `0` | ✅ discriminates — use this |
| `journalctl -u <u> -n 0` exit code | `0` | `0` | ❌ both zero — useless |
| `test -L /etc/systemd/system/multi-user.target.wants/<u>.service` | exit 0 | exit 1 | ✅ also works for enabled units; fails for static/oneshot units |

**Decision**: probe = `journalctl -u <u> -n 1 --output=cat --no-pager` stdout-byte-count > 0. Fallback: `find /etc/systemd/system /aarch64-bottlerocket-linux-gnu/sys-root/usr/lib/systemd/system -maxdepth 3 -name "<u>.service"` non-empty.

### tcpdump pcap-flush on SPDY-cancel

5 trial runs each, target = `metrics-server` pod, captured for ~1s then SPDY-cancelled mid-flight:

| Variant | Valid pcap rate |
|---|---|
| Plain `tcpdump -U -w -` (no wrapper, SPDY-cancel) | 5/5 valid |
| `sh -c 'trap kill -INT $TPID; tcpdump ... & wait'` (signal-forwarding wrapper) | 5/5 valid |
| `timeout -s INT -k 2 <dur> tcpdump -U -w -` (busybox timeout, full duration) | 3/3 valid |

**Decision**: SPDY-cancel of `tcpdump -U -w -` produces valid pcaps (header + per-packet flush). Round-1 codex concern overstated. **Use busybox `timeout` for duration cap** (GNU `--signal=` syntax doesn't exist in netshoot's busybox), **and** rely on SPDY-cancel for early Ctrl-C. No wrapper needed.

Critical caveat: netshoot ships **busybox `timeout`**, syntax `timeout -s INT -k <kill-after> <dur> <cmd>`. NOT GNU's `--signal=INT --kill-after=N`. Original DESIGN.md syntax broken.

### Runtime endpoint discovery (configz)

```
kubectl get --raw /api/v1/nodes/<node>/proxy/configz
  | jq -r .kubeletconfig.containerRuntimeEndpoint
```
Both nodes returned `unix:///run/containerd/containerd.sock`. Discovery path is one apiserver-proxy GET per session, cached. Replaces hardcoded socket path.

## Outstanding

Cleanup pod removed: `kubectl delete pod auto-debug-verify -n kube-system --grace-period=0 --force` ✅
