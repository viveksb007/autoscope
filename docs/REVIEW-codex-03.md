# Codex Review #3 — rev3

Date: 2026-06-04
Reviewer: codex-cli 0.136.0 (yolo mode)

## Status: NOT YET CONSENSUS

0 BLOCKERS. 6 HIGH (3 STILL OPEN + 2 NEW + 1 IMPL-READY).

## STILL OPEN
- **HIGH** — runtime endpoint discovery only stated; `buildPodSpec` and Phase 1 still hardcode socket path. `DESIGN.md:62,244,260`, `PLAN.md:19`.
- **HIGH** — namespace bootstrap conflict: REQ says idempotent label set; TOOL/DESIGN say `--auto-label` required to patch existing. `REQUIREMENTS.md:85`, `TOOL.md:31`, `DESIGN.md:267`.
- **HIGH** — `--via` precedence ambiguous in TOOL.md. `TOOL.md:130-137`.
- **MEDIUM** — PLAN risks line still references broken `journalctl -n 0` probe. `PLAN.md:97`.
- **MEDIUM** — catalog drift: DESIGN has `ebs-csi-registrar`, TOOL omits.

## NEW
- **HIGH** — DESIGN Concurrency section still says GNU `timeout --signal=INT --kill-after=2`. tcpdump section corrected; Concurrency section stale. `DESIGN.md:370`.
- **HIGH** — `--auto-label` patches existing ns ⇒ needs `namespaces:patch` (or `update`); RBAC table omits.
- **MEDIUM** — `auto install` synopsis inconsistent: TOOL has `--auto-label`, DESIGN doesn't.
- **MEDIUM** — `--duration 0` unlimited not explicitly designed for; busybox timeout always wrapped.
- **MEDIUM** — target-pod watcher missing `resourceVersion` start point.

## IMPL-READY
- **HIGH** — `buildPodSpec` API takes no discovered socket path. Phase 1 trips.
- **HIGH** — preflight RBAC missing `namespaces:patch`. Phase 0.5 trips.
- **HIGH** — DESIGN Concurrency timeout syntax wrong.
- **MEDIUM** — Phase 4 `duration=0` + watcher race.

## Reconciliation rev4
1. Update DESIGN buildPodSpec signature to `(node, ns, imageDigest, runtimeSocket string)`. PLAN Phase 1 mirrors.
2. Reconcile `auto install`: TOOL+DESIGN+REQ all say "create new ns with PSA label always; existing ns: patch only if `--auto-label`, else refuse". RBAC adds `namespaces:patch`.
3. TOOL.md `--via` precedence: explicit ordered list — `--port/--path` ⇒ node; `--via` ⇒ transport per flag value; `--endpoint NAME` ⇒ catalog Transport for that endpoint; default ⇒ catalog Transport for `metrics` endpoint. Document.
4. PLAN risks: replace stale liveness probe text with verified `journalctl -n 1` byte-count.
5. Catalog: align TOOL.md to DESIGN.md (or vice versa). Pick TOOL canonical, prune DESIGN to match.
6. DESIGN Concurrency: fix `timeout --signal=` → busybox syntax.
7. tcpdump `--duration 0`: design no-timeout branch — skip the busybox wrapper, rely solely on Ctrl-C / target-pod watcher / debug-pod watcher.
8. Target-pod watcher: spec `Watch` from `resourceVersion=fetchedPod.ResourceVersion` to avoid race. Document.
