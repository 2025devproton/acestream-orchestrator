# Testing Guide: Dynamic VPN Port Allocation

## Recovery regression suite

```bash
cd app/orchestrator
go test -race ./...
cd ../..
python3 -m unittest discover -s tests -v
docker build -t acestream-orchestrator:recovery-review .
```

Tests use fake Docker responses, a disposable Redis test server, HTTP streaming
servers, and a TCP engine-protocol stub. They do not need production containers
or VPN credentials. Cases include cleanup ordering and retries, lease/port
retention, static-node protection, missing nodes, unhealthy timestamp transitions,
partial chunks, startup grace, cooldown, bounded restarts, slow HTTP negotiation,
API session replacement/cancellation, probe policies, and supervisor exit codes.

For live acceptance, use an isolated Docker environment and dedicated credentials.
Stop and remove a dynamic VPN separately, repeat with missed events/reindexing,
and verify resource release plus restored desired capacity. Exercise genuine
P2P gaps and both playback modes with viewers attached. Do not infer live VPN
recovery or media continuity from the stub-based tests alone.

This guide validates dynamic VPN node provisioning and host-port distribution in orchestrator-managed mode.

## Preconditions

1. Start with `docker-compose.yml`.
2. Configure VPN in Settings (provider/protocol/credentials).
3. Ensure host port exposure covers your configured range (for example `19000-19999`).

Optional range slot configuration:

```bash
GLUETUN_PORT_RANGE_1=19000-19499
GLUETUN_PORT_RANGE_2=19500-19999
PORT_RANGE_HOST=19000-19999
```

## Basic Verification

### 1. Confirm dynamic nodes exist

```bash
curl -s http://localhost:8000/orchestrator/status | jq
```

### 2. Confirm engines are assigned to dynamic VPN nodes

```bash
curl -s http://localhost:8000/engines | jq
```

Expected:
- Engine entries include `vpn_container` names like `gluetun-dyn-*`.
- Engine `host` matches the assigned VPN container when VPN is enabled.

### 3. Confirm per-node forwarded engine behavior

```bash
curl -s http://localhost:8000/engines | jq '[.[] | select(.forwarded == true)]'
```

Expected:
- At most one forwarded engine per VPN node.

## Port Range Slot Validation

If range slots are configured, verify each engine host port falls into the slot assigned to its VPN node.

Manual check example:

```bash
curl -s http://localhost:8000/engines | jq '.[] | {container_name, vpn_container, port, host}'
```

## Failure/Recovery Validation

1. Temporarily invalidate one VPN credential (or stop one managed VPN container).
2. Verify affected node becomes NotReady/Down and engines are evicted from that node.
3. Restore credentials/connectivity.
4. Verify controller recreates/reconciles nodes and engine capacity returns.

## Troubleshooting

- No dynamic nodes: verify VPN settings and credentials.
- No engines scheduled: check node readiness and `/orchestrator/status`.
- Port allocation errors: verify host range and non-overlapping slot ranges.
