# `auto` — Security & Blast Radius

## TL;DR

**`auto` creates a privileged hostPID/hostNetwork/hostIPC pod on the target node. This is functionally equivalent to root access on that node, and equivalent to cluster-admin in effect** because the pod can read every secret on the node, modify the host filesystem (via `nsenter -t 1 -m`), and exfiltrate arbitrary host data through the API server's exec stream.

If your cluster's threat model treats `pods:create` with privileged admission as a **node-takeover primitive**, `auto` does not change that — it merely automates a pattern operators were already using by hand.

## Trust model

- **Caller**: a human or automation with a kubeconfig and the RBAC verbs listed below. Treated as trusted within the scope of `pods:create` on the target namespace.
- **Cluster**: presumed honest. `auto` does not defend against a malicious apiserver.
- **Node**: presumed honest at OS layer; `auto` reads from it but does not protect against compromised Bottlerocket images.
- **Image**: `nicolaka/netshoot@sha256:<digest>` pinned; override with `--image @sha256:...`.

## RBAC required

| Resource | Verbs | Used by |
|---|---|---|
| `pods` (in target ns) | `create, get, list, delete, patch, watch` | debug pod lifecycle; TTL refresh via strategic-merge-patch; readiness via watch |
| `pods/exec` (in target ns) | `create` | every subcommand |
| `nodes/proxy` | `get` | kubelet metrics + `configz` runtime endpoint discovery |
| `namespaces` | `create, get` | `auto install` baseline |
| `namespaces` | `patch` | `auto install --auto-label` (relaxes PSA on existing ns) |

Minimum ClusterRole:

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: auto-debugger
rules:
- apiGroups: [""]
  resources: ["pods"]
  verbs: ["create", "get", "list", "delete", "patch", "watch"]
- apiGroups: [""]
  resources: ["pods/exec"]
  verbs: ["create"]
- apiGroups: [""]
  resources: ["nodes/proxy"]
  verbs: ["get"]
- apiGroups: [""]
  resources: ["namespaces"]
  verbs: ["create", "get", "patch"]
```

## Pod Security Admission

The target namespace must allow privileged pods:

```
pod-security.kubernetes.io/enforce=privileged
```

`auto install` sets this label idempotently when creating the namespace. For an existing namespace lacking the label, `auto install --auto-label` patches it (refusal mode by default — protects shared namespaces from accidental relaxation).

On clusters that block PSA-privileged at the cluster level (validating webhook, OPA, Kyverno), `auto` cannot operate without a policy exception.

## What's mounted

Only one host bind-mount: the containerd UNIX socket at the path discovered via kubelet `/configz` (defaults to `/run/containerd/containerd.sock`). No host filesystem mount, no `/proc` mount, no `/var/log` mount.

Host journals, host binaries (`journalctl`, `systemctl`, `ctr`), and host processes are reached transparently via `nsenter -t 1 -m -u -i` from within the pod's mount namespace.

## What's not protected

`auto` is **not** a sandbox.

- The privileged pod can do anything root can do on the node.
- `auto exec <node> -- <cmd>` will run any command, including state-modifying ones. The CLI prints a stderr banner but does not prevent it.
- pcap captures may include cleartext credentials, tokens, or sensitive payloads. Treat output files as you would any traffic capture.
- `auto metrics` via apiserver-proxy returns whatever the kubelet exposes; some endpoints leak resource details.

## Guardrails available

- `--require-cluster-suffix STR` — refuses unless current kubeconfig context name ends with `STR`. Recommended global flag for production safety.
- `auto.debugger/protect=true` node label — *not yet enforced* in v0.1; planned.
- `--namespace` defaults to `auto-debug` (separate from `kube-system`); use this to keep blast radius scoped.
- `auto cleanup --all --yes` — explicit teardown; recommend running at session end even though TTL-based opportunistic GC is automatic on every command.

## Audit trail

Each debug pod carries:

- `auto.debugger/created-by` annotation — caller's local username (`os/user.Current()`).
- `auto.debugger/ttl-epoch` label — UNIX seconds of expiry; refreshed on every `Ensure`.
- `auto.debugger/node-hash` label — sha8 of the node name (full name in `auto.debugger/node` annotation).

API audit logs on the apiserver capture every exec stream. Container logs on the debug pod itself only contain the `sleep 86400` PID 1 command.

## Operator checklist

Before running `auto` on a production cluster:

1. Verify the active kubeconfig context with `kubectl config current-context`.
2. Confirm `--require-cluster-suffix` is set to your prod-cluster suffix (e.g. `prod.us-east-1.eksctl.io`).
3. Use `--namespace auto-debug` (default) — never `kube-system` unless your cluster-admin has signed off.
4. Pin a known-good image digest via `--image nicolaka/netshoot@sha256:...`.
5. After the session: `auto cleanup --all --yes`.
6. Review apiserver audit logs for the session window.

## Known v1 risks

- `nicolaka/netshoot:latest` may be defaulted if `--image` not pinned and the build wasn't shipped with a baked-in digest. Pin explicitly.
- `auto.debugger/protect=true` node-label refusal is documented but not enforced yet. Track separately if you rely on it.
- No `--dry-run` mode — every command that creates resources actually creates them.
