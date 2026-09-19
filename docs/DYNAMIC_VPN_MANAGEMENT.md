# Dynamic VPN Management

This document defines the current VPN architecture for AceStream Orchestrator.

## Executive Summary

AceStream Orchestrator manages Gluetun VPN nodes dynamically using a declarative controller.

- VPN nodes are created and recycled by reconciliation loops.
- User-provided credentials are managed as finite leases.
- Engine placement and failover behavior are lease-aware and forwarding-aware.

> [!WARNING]
> Static compose-driven VPN topologies are deprecated for primary operations. Do not rely on fixed single/redundant Gluetun compose stacks as the runtime control model.

## Credential Lease System

### Recovery and container ownership

VPN containers are excluded from engine discovery even when both share a managed
label. Lifecycle ownership requires `acestream-orchestrator.managed=true` and
`role=vpn_node`, plus `acestream.vpn.dynamic=true` for newly provisioned nodes.
Legacy containers with those management labels and a `gluetun-dyn-` name remain
supported. A name prefix or role alone never authorizes deletion. Static/external
VPNs are not destroyed by recovery, startup cleanup, or shutdown cleanup; their
drain/delete API requests return HTTP 409 while tracked as external.

Stopped, exited, dead, and missing owned nodes retain recovery state through
Docker event processing and reindexing. Missing nodes receive a 30-second startup
grace. Cleanup uses exact names, verifies stored container IDs and ownership,
removes dependent managed engine containers first, and only then removes the VPN.
Docker errors retain the credential and AirVPN port for retry. Renamed containers
or unexpected identity changes require operator reconciliation rather than forced
deletion. Recovered capacity is recreated by the normal scaling loop.

Healing uses `UnhealthySince`, independent of observation timestamps. Refreshing
the same unhealthy container preserves its interval; verified recovery clears it,
and a different container ID starts a new grace period. VPN state readers receive
snapshots rather than writable pointers into shared state.

At startup, leases are restored from both running and stopped owned containers
before provisioning starts. An unavailable Docker snapshot fails startup instead
of allocating credentials from an incomplete view.

Credentials are not passive configuration entries. Each credential is a schedulable lease.

### Lifecycle

1. Credential is added in Settings -> VPN.
2. Controller marks credential as available for lease.
3. Dynamic VPN node claims one lease during provisioning.
4. Lease is released when node is drained/destroyed.
5. Lease can be reused for replacement nodes.

### Capacity rule

The maximum number of simultaneously active VPN nodes is bounded by available credential leases.

## Blue/Green Predictive Pre-Warming

When a VPN node is unstable or approaching replacement conditions:

1. Existing node is marked `draining` (Blue).
2. New node is provisioned immediately (Green) from spare lease capacity.
3. Scheduler stops assigning new engines to Blue.
4. Active stream sessions are migrated away from Blue.
5. Blue is retired after workload evacuation.

This strategy reduces disruptive hard failovers and preserves availability under churn.

## Per-Credential Port Forwarding

Each WireGuard credential supports a `port_forwarding` capability flag.

### Scheduling implications

- `port_forwarding=true`: credential is eligible for forwarding-leader workloads.
- `port_forwarding=false`: credential remains usable for non-forwarded placement.

The scheduler enforces this at placement time, preventing accidental leader election on credentials that cannot support forwarding.

## Provisioning Guardrails

Provisioning can be intentionally blocked when prerequisites are missing.

Common blocked states:

- No healthy dynamic VPN node exists.
- No available credential lease exists.
- Forwarded-leader placement requested but no forwarding-capable lease is available.

These are expected protective behaviors, not silent failures.

## Operational Guidance

### Required operator workflow

1. Start orchestrator with `docker-compose.yml`.
2. Open `/panel`.
3. Add valid WireGuard credentials in Settings -> VPN.
4. Confirm at least one lease is usable.
5. Enable VPN-backed provisioning.

### Monitoring signals

- `/orchestrator/status` for control-plane readiness and blocked reasons.
- `/engines` for lifecycle state (`active`, `draining`) and forwarding flags.
- Dashboard VPN views for lease and node visibility.

## Related Docs

- [ARCHITECTURE.md](ARCHITECTURE.md)
- [CONFIG.md](CONFIG.md)
- [DEPLOY.md](DEPLOY.md)
- [PANEL.md](PANEL.md)
- [TESTING_GUIDE.md](TESTING_GUIDE.md)
