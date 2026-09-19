# Improve dynamic VPN recovery, stalled stream handling, and runtime health checks

## Summary

This change applies the useful recovery behavior from the downstream patches and
addresses the lifecycle, ownership, retry, and observability issues found during
review.

It improves dynamic VPN recovery, adds bounded opt-in stream recovery, separates
liveness from serving and provisioning readiness, and makes critical runtime
process failures visible to Docker's restart policy.

## Problem

- VPN containers sharing the engine management label could be registered as both
  VPNs and engines.
- Stopped or missing VPNs could lose their recovery state during reindexing.
- Docker cleanup failures could release credentials while containers remained.
- Healing used `LastSeen`, which can be refreshed while a node remains unhealthy.
- Open stream connections could receive bytes without producing complete buffer
  chunks, leaving viewers stalled indefinitely.
- A queued stream recovery could restart a newly recovered session.
- The health endpoint reported API liveness but was not sufficient to express
  serving capacity or provisioning availability.
- The startup supervisor could exit successfully after a critical child failed.

## Changes

### Dynamic VPN recovery

- Centralize VPN role and ownership classification.
- Exclude VPN containers from engine discovery.
- Require management labels and dynamic ownership before destructive cleanup.
- Add `acestream.vpn.dynamic=true` to newly provisioned nodes while retaining
  compatibility with labelled legacy `gluetun-dyn-*` containers.
- Preserve recovery records across stop events and periodic reindexing.
- Protect external VPNs from automatic deletion and return HTTP 409 for their
  drain/delete requests.
- Verify exact names, recorded IDs, and ownership before removal.
- Remove dependent managed engines before removing the VPN container.
- Retain credentials and AirVPN ports when Docker operations fail; release them
  only after confirmed cleanup.
- Preserve `UnhealthySince` across same-container updates and reset it for a new
  container identity.
- Restore leases from running and stopped owned nodes before provisioning starts.

### Stream recovery

- Track complete ring-buffer chunk progress separately from partial writes.
- Run stall detection in the active reader-session loop, preventing stale queued
  recovery signals from affecting a replacement session.
- Add initial grace, cooldown, and a finite recovery budget.
- Keep viewers connected during successful session replacement.
- Make legacy API connections cancellation-aware.
- Add bounded-cardinality Prometheus counters and recovery-duration metrics.

Stream recovery is disabled by default and can be enabled with:

```text
STREAM_STALL_TIMEOUT_S=25
```

Related settings are `STREAM_STALL_CHECK_INTERVAL_S`,
`STREAM_STALL_COOLDOWN_S`, and `STREAM_STALL_MAX_RECOVERIES`. The signal measures
completed buffer chunks, not verified media decodability.

### Health checks and supervision

- Add `app/readiness.py` with explicit liveness, serving-readiness, minimum
  replica, and provisioning modes.
- Validate response schema and capacity arithmetic without exposing response
  bodies or credentials.
- Add a Docker liveness healthcheck.
- Move the startup supervisor into `app/start.py`.
- Monitor Redis and the Go process as foreground children and return exit code 1
  when either critical process exits unexpectedly.
- Add CI for the race-enabled Go suite and Python probe/supervisor tests.

## Compatibility decisions

Stream recovery remains opt-in. API-key configuration, the default runtime user,
and the local-source Docker build remain compatible with the existing Compose
deployment. The downstream bundle's mandatory API key, fixed non-root UID/GID,
and remote pinned-source Dockerfile are intentionally excluded because they need
separate deployment decisions.

External VPNs are not automatically destroyed. Unexpected container identity
changes retain the lease and state for operator reconciliation. An unavailable
Docker lease snapshot fails startup instead of allowing unsafe provisioning.

## Validation

Executed locally on Linux amd64:

```bash
cd app/orchestrator
go test -race ./...
cd ../..
python3 -m unittest discover -s tests -v
docker build -t acestream-orchestrator:recovery-review .
git diff --check
```

The Go race-enabled suite and 12 Python tests pass. The Docker image build runs
the full Go suite and builds the unified Linux binary. Image smoke tests verified
probe imports, the liveness probe, healthcheck metadata, and nonzero supervisor
exit when Docker is unavailable.

## Remaining acceptance work

Automated Docker behavior uses fake clients, while playback tests use local HTTP
and TCP stubs. Production VPN credentials, real P2P playback continuity,
destructive recovery against live containers, and arm64 image execution still
require validation in an isolated environment.

Recommended live checks include missed Docker events, stopped versus removed VPN
containers, intermittent Docker failures, active viewers in both control modes,
and threshold tuning for real bitrate and P2P gaps.
