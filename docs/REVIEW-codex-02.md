# Codex Review #2 — rev2 Docs

Date: 2026-06-04
Reviewer: codex-cli 0.136.0 (yolo mode)
Files reviewed: `REQUIREMENTS.md`, `DESIGN.md`, `PLAN.md`, `VERIFY.md`, `REVIEW-codex-01.md`

## CLOSED (round-1 items resolved)

- BLOCKER closed — `nsenter -t 1 -a` replaced by split namespace usage. `REQUIREMENTS.md:97`, `DESIGN.md:113`, `VERIFY.md:44`.
- BLOCKER closed — `crictl inspect` replaced with host `ctr -n k8s.io tasks list`. `REQUIREMENTS.md:102`, `DESIGN.md:128`, `VERIFY.md:61`.
- BLOCKER closed — kubelet metrics now apiserver-proxy default. `REQUIREMENTS.md:105`, `DESIGN.md:179`, `VERIFY.md:90`.
- HIGH closed — writable hostPath `/` dropped; only containerd socket remains. `DESIGN.md:58,72,336`.
- HIGH closed — `/host` dm-verity fallback gone with host filesystem mount. `DESIGN.md:72`.
- HIGH closed — long-node label fixed via `node-hash` + annotation. `DESIGN.md:62`.
- HIGH closed — list-create race fixed via deterministic pod name + AlreadyExists retry. `DESIGN.md:81`.
- HIGH closed — "pick first reusable" fixed by one deterministic pod per node. `DESIGN.md:83,92`.
- HIGH closed — TTL implementation path via startup sweep. `DESIGN.md:99`.
- HIGH closed — SPDY-cancel limitation documented; tcpdump duration uses remote `timeout`. `DESIGN.md:318,162`.
- HIGH closed — multi-container tcpdump selection specified. `DESIGN.md:148`.
- HIGH closed — jq/runtime JSON dep removed; parse `ctr` output in Go. `DESIGN.md:130`.
- HIGH closed — image pinning required in design+plan. `DESIGN.md:53,331`, `PLAN.md:7`.
- HIGH closed — privileged hostPID blast radius documented. `DESIGN.md:327`.
- HIGH closed — read-only concern addressed by dropping host fs mount. `DESIGN.md:72,336`.

## STILL OPEN (round-1, not addressed)

- **HIGH** — runtime endpoint discovery only stated in REQUIREMENTS; design/plan still hardcode `/run/containerd/containerd.sock` + `ctr` without applying `configz` discovery. `REQUIREMENTS.md:103`, `DESIGN.md:59,121`, `PLAN.md:38`.
- **HIGH** — RBAC underspecified: kubelet metrics needs `nodes/proxy:get`, NFR1 only lists `nodes get,list`; namespace creation/PSA verbs missing. `REQUIREMENTS.md:66,105`, `DESIGN.md:330`.
- **HIGH** — target-pod death/restart handling not designed; FR1 requires stop on pod death, design only watches debug pod and resolves target PID once. `REQUIREMENTS.md:26`, `DESIGN.md:159,175`.
- **HIGH** — pcap validity on Ctrl-C still not guaranteed; remote `timeout` covers duration only, not early Ctrl-C. `REQUIREMENTS.md:26`, `DESIGN.md:177,320`.

## NEW (rev2-introduced)

- **BLOCKER** — `journalctl -u <u> --since 1m -n 0 --output=cat` exit-code probe doesn't discriminate. `-n 0` always produces no output by definition. Need different probe (e.g. `journalctl --list-boots` is not unit-aware; try `journalctl -u <u> --since 1m -n 1 --output=cat` and check stdout-non-empty OR `journalctl --quiet -u <u> -n 0` exit-code semantics; or list `/etc/systemd/system/multi-user.target.wants/<u>.service` symlink existence — the live-verified path). `REQUIREMENTS.md:42`, `DESIGN.md:195`, `VERIFY.md:128`.
- **HIGH** — verified manifest in VERIFY (kube-system, `:latest`, broad `Exists` toleration) ≠ designed manifest (auto-debug, digest pin, narrow tolerations). `DESIGN.md:42,47,53` vs `VERIFY.md:13,20,23`.
- **HIGH** — namespace contradicts itself: design "auto-created on first run", security "`auto install` not implemented, user pre-creates", plan has no creation phase. `DESIGN.md:69,330`, `PLAN.md:14`.
- **HIGH** — metrics endpoint behavior conflicts for agents with no metrics endpoint: REQS says "fail fast with healthz hint", DESIGN says "falls back per-agent", catalog has `pod-identity` healthz-only. `REQUIREMENTS.md:36`, `DESIGN.md:181,282`.
- **HIGH** — TOOL.md required but absent. REQS+DESIGN alone are not a complete agent SOP. `REQUIREMENTS.md:85`, `PLAN.md:10`.
- **HIGH** — agent CLI contract underspecified without TOOL.md: `--endpoint`/`--via`/`--port`/`--path` precedence + fallback spread across conflicting text. `REQUIREMENTS.md:28,32`, `DESIGN.md:181,299`.
- **MEDIUM** — DESIGN architecture diagram still shows `nsenter -t 1 -a` + host `curl/tcpdump/crictl`, contradicted by VERIFY. `DESIGN.md:14,20`.
- **MEDIUM** — TTL semantics conflict: REQS says "No automatic GC in v1", DESIGN adds startup `SweepExpired`. `REQUIREMENTS.md:60`, `DESIGN.md:99`.

## IMPL-READY (Phase 1 trip hazards)

- **BLOCKER** — `PLAN.md:55` Phase 4 still says `crictl inspect`. Direct contradiction with `ctr` decision. `PLAN.md:55` vs `VERIFY.md:40,76`, `DESIGN.md:128`.
- **HIGH** — Phase 1 trips on namespace: `buildPodSpec(ns)` assumes ns; no phase creates/labels `auto-debug` or implements `auto install`. `PLAN.md:18`, `DESIGN.md:69,330`.
- **HIGH** — Phase 1/3 trips on RBAC preflight: `nodes/proxy`, namespace verbs, admission checks missing from NFR1. `REQUIREMENTS.md:66,105`, `PLAN.md:97`.
- **HIGH** — Phase 3 trips on `logs` liveness probe — broken as written. `DESIGN.md:195`.
- **HIGH** — Phase 4 trips on Ctrl-C pcap-validity if acceptance requires it. `REQUIREMENTS.md:26`, `DESIGN.md:162,177`.
- **MEDIUM** — Phase 6 compliance blocked until TOOL.md + COMPLIANCE.md exist. `PLAN.md:10,80`.

## Summary

3 blockers from round-1 closed. 1 new BLOCKER (broken liveness probe). 4 round-1 HIGHs still open + 6 new HIGHs. 1 IMPL-READY BLOCKER (PLAN.md:55 stale crictl text).

## Reconciliation Plan (rev3)

| # | Issue | Fix |
|---|---|---|
| 1 | journalctl probe broken | Replace with: probe symlink `/etc/systemd/system/*.target.wants/<unit>.service` existence via `nsenter -t 1 -m -u -i -- /usr/bin/test -L <path>` (verified live: 16 symlinks listed). Falls back to `journalctl -u <u> -n 1 --output=cat` content non-empty for unit-files in non-standard paths. |
| 2 | VERIFY≠DESIGN manifest | Add disclaimer in VERIFY.md "manifest used for verification differs from final design — see DESIGN.md for production manifest. Differences: ns=kube-system→auto-debug, image=:latest→@sha256:digest, tolerations broadened for verify-only". |
| 3 | namespace behavior | Add `auto install` / `auto bootstrap` command to v1: creates `auto-debug` ns labeled `pod-security.kubernetes.io/enforce=privileged`, idempotent. PLAN Phase 0.5 covers it. Default `Ensure` lazily calls bootstrap if ns missing. |
| 4 | metrics fallback | Decision: `--endpoint NAME` is required when agent has no `metrics` endpoint (e.g. pod-identity). No silent fallback. Update REQS+DESIGN. |
| 5 | TOOL.md absent | Author TOOL.md as Phase 0.5 deliverable BEFORE Phase 1 begins. Add to `docs/`. |
| 6 | CLI contract spread | Move all flag schemas + precedence rules into TOOL.md as canonical source; REQS/DESIGN reference TOOL.md. |
| 7 | architecture diagram stale | Redraw with split nsenter flags + ctr lookup; remove host curl/tcpdump/crictl. |
| 8 | TTL contradiction | Reword REQS FR6 to "Automatic best-effort GC at startup; no background CronJob in v1". |
| 9 | runtime endpoint discovery | Add `internal/kube/runtime.go`: cached per-node `configz` lookup; pod spec uses discovered socket path. PLAN Phase 1 owns it. |
| 10 | RBAC list | Expand NFR1 + add SECURITY.md table: `pods` (create/get/list/delete in target ns), `pods/exec` (create), `pods/log` (get), `pods/status` (get), `nodes` (get/list), `nodes/proxy` (get), `namespaces` (create — for `auto install` only), `selfsubjectaccessreviews` (create — for preflight). |
| 11 | target pod death | tcpdump command starts a goroutine watching target pod via `clientset.CoreV1().Pods(ns).Watch`; on `Deleted`/`Pending`/`Failed`/restart-counter-bump → cancel context. PID re-resolution NOT done; capture ends. |
| 12 | pcap Ctrl-C validity | Wrapper: `nsenter ... -- sh -c 'trap "kill -INT \$PID" INT TERM; tcpdump ... & PID=\$!; wait \$PID'` with explicit signal forwarding. Pair with remote `timeout` for duration cap. (Bottlerocket `brush` shell only matters for host PID 1; debug pod has full sh — this wrapper is fine inside the debug pod's mount-ns when running `nsenter -t <wpid> -n` since shell binary is netshoot's `/bin/sh`.) |
| 13 | PLAN.md:55 crictl stale | Edit Phase 4 step 1 to "Resolve target pod → node + container PID via `ctr -n k8s.io tasks list`". |

All BLOCKER + HIGH applied. MEDIUMs accepted.
