#!/usr/bin/env python3
"""Probe API liveness, serving readiness, or provisioning availability."""

from __future__ import annotations

import argparse
import math
import os
import sys
from dataclasses import dataclass
from typing import Any, Mapping, Optional
from urllib.error import HTTPError, URLError
from urllib.parse import urljoin
from urllib.request import Request, urlopen


HEALTH_PATH = "/api/v1/health/status"
STATUS_PATH = "/api/v1/orchestrator/status"
DEFAULT_URL = "http://127.0.0.1:8000"
DEFAULT_TIMEOUT = 3.0
MAX_RESPONSE_BYTES = 1024 * 1024


class ProbeError(Exception):
    """Safe probe failure; response bodies and credentials are never retained."""

    def __init__(self, path: str, reason: str) -> None:
        self.path = path
        super().__init__(reason)


class SchemaError(Exception):
    """The status response is missing or has invalid fields."""


@dataclass(frozen=True)
class Result:
    state: str
    reason: str
    details: Mapping[str, Any]

    @property
    def ready(self) -> bool:
        return self.state == "healthy"

    @property
    def exit_code(self) -> int:
        return 0 if self.ready else 1

def _mapping(value: Any, name: str) -> Mapping[str, Any]:
    if type(value) is not dict:
        raise SchemaError(f"missing or invalid {name}")
    return value


def _integer(payload: Mapping[str, Any], name: str) -> int:
    value = payload.get(name)
    if type(value) is not int or value < 0:
        raise SchemaError(f"missing or invalid {name}")
    return value


def _boolean(payload: Mapping[str, Any], name: str) -> bool:
    value = payload.get(name)
    if type(value) is not bool:
        raise SchemaError(f"missing or invalid {name}")
    return value


def _string(payload: Mapping[str, Any], name: str) -> str:
    value = payload.get(name)
    if type(value) is not str:
        raise SchemaError(f"missing or invalid {name}")
    return value


def evaluate(payload: Mapping[str, Any], require_minimum: bool = False,
             provisioning_only: bool = False) -> Result:
    """Evaluate a consolidated status snapshot using an explicit probe policy."""

    try:
        root = _mapping(payload, "status payload")
        _string(root, "status")

        engines = _mapping(root.get("engines"), "engines")
        engine_total = _integer(engines, "total")
        engine_healthy = _integer(engines, "healthy")
        engine_unhealthy = _integer(engines, "unhealthy")
        engine_draining = _integer(engines, "draining")
        if engine_healthy + engine_unhealthy + engine_draining > engine_total:
            raise SchemaError("inconsistent engine counts")

        capacity = _mapping(root.get("capacity"), "capacity")
        capacity_total = _integer(capacity, "total")
        capacity_used = _integer(capacity, "used")
        capacity_available = _integer(capacity, "available")
        min_replicas = _integer(capacity, "min_replicas")
        max_replicas = _integer(capacity, "max_replicas")
        if capacity_used > capacity_total or capacity_available > capacity_total:
            raise SchemaError("inconsistent capacity")
        if min_replicas > max_replicas:
            raise SchemaError("invalid replica limits")
        if capacity_total != engine_total or capacity_used + capacity_available != capacity_total:
            raise SchemaError("inconsistent engine capacity")

        vpn = _mapping(root.get("vpn"), "vpn")
        vpn_enabled = _boolean(vpn, "enabled")
        vpn_total = _integer(vpn, "nodes_total")
        vpn_healthy = _integer(vpn, "healthy")
        if vpn_healthy > vpn_total:
            raise SchemaError("inconsistent VPN counts")

        provisioning = _mapping(root.get("provisioning"), "provisioning")
        can_provision = _boolean(provisioning, "can_provision")
        circuit_breaker = _string(provisioning, "circuit_breaker_state")

        details = {
            "engines_total": engine_total,
            "engines_healthy": engine_healthy,
            "engines_unhealthy": engine_unhealthy,
            "engines_draining": engine_draining,
            "min_replicas": min_replicas,
            "max_replicas": max_replicas,
            "capacity_available": capacity_available,
            "vpn_required": vpn_enabled,
            "vpn_nodes_healthy": vpn_healthy,
            "can_provision": can_provision,
            "circuit_breaker_state": circuit_breaker,
        }

        provision_ready = can_provision and circuit_breaker == "closed" and max_replicas > 0
        details["provisioning_available"] = provision_ready
        if provisioning_only:
            return Result("healthy" if provision_ready else "unavailable",
                          "provisioning permitted" if provision_ready else "provisioning blocked", details)
        if engine_total == 0 and min_replicas == 0 and provision_ready:
            details["mode"] = "idle"
            return Result("healthy", "idle; lazy engine/VPN provisioning permitted", details)
        if vpn_enabled and vpn_healthy == 0:
            return Result("unavailable", "no healthy VPN node is available", details)
        if require_minimum and engine_healthy < min_replicas:
            return Result(
                "unavailable",
                "healthy engine capacity is below the configured minimum",
                details,
            )
        if engine_healthy == 0:
            return Result("unavailable", "all provisioned engines are unavailable", details)
        details["mode"] = "active"
        reason = "healthy engines can serve; provisioning is degraded" if not provision_ready else "healthy engines can serve"
        return Result("healthy", reason, details)
    except SchemaError as exc:
        return Result("unavailable", str(exc), {})


def _get_json(base_url: str, path: str, timeout: float) -> Mapping[str, Any]:
    request = Request(
        urljoin(base_url.rstrip("/") + "/", path.lstrip("/")),
        headers={"Accept": "application/json"},
        method="GET",
    )
    try:
        with urlopen(request, timeout=timeout) as response:
            if not 200 <= response.status < 300:
                raise ProbeError(path, f"HTTP {response.status}")
            raw = response.read(MAX_RESPONSE_BYTES + 1)
    except HTTPError as exc:
        raise ProbeError(path, f"HTTP {exc.code}") from None
    except (URLError, TimeoutError, OSError):
        raise ProbeError(path, "request failed") from None
    if len(raw) > MAX_RESPONSE_BYTES:
        raise ProbeError(path, "response too large")
    try:
        import json

        value = json.loads(raw.decode("utf-8"))
    except (UnicodeDecodeError, ValueError):
        raise ProbeError(path, "invalid JSON") from None
    if type(value) is not dict:
        raise ProbeError(path, "JSON object expected")
    return value


def probe(base_url: str, timeout: float = DEFAULT_TIMEOUT, liveness: bool = False,
          require_minimum: bool = False, provisioning_only: bool = False) -> Result:
    """Probe one public liveness or consolidated readiness endpoint."""

    path = HEALTH_PATH if liveness else STATUS_PATH
    try:
        payload = _get_json(base_url, path, timeout)
    except ProbeError as exc:
        return Result("unavailable", f"{exc.path}: {exc}", {})
    except ValueError:
        return Result("unavailable", "invalid probe URL", {})
    if liveness:
        if payload.get("status") != "healthy":
            return Result("unavailable", "invalid liveness response", {})
        return Result("healthy", "control API is live", {"mode": "liveness"})
    return evaluate(payload, require_minimum, provisioning_only)


def _format(result: Result) -> str:
    if result.ready and result.details.get("mode") == "idle":
        return f"READY: healthy idle ({result.reason})"
    prefix = "READY" if result.ready else "NOT READY"
    return f"{prefix}: {result.state} ({result.reason})"


def main(argv: Optional[list[str]] = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--url", default=os.environ.get("ORCHESTRATOR_URL", DEFAULT_URL))
    parser.add_argument("--timeout", type=float, default=DEFAULT_TIMEOUT)
    modes = parser.add_mutually_exclusive_group()
    modes.add_argument("--liveness", action="store_true")
    modes.add_argument("--provisioning", action="store_true")
    parser.add_argument("--require-min-replicas", action="store_true")
    args = parser.parse_args(argv)
    if not math.isfinite(args.timeout) or args.timeout <= 0:
        result = Result("unavailable", "invalid probe timeout", {})
    else:
        result = probe(args.url, args.timeout, args.liveness, args.require_min_replicas, args.provisioning)
    print(_format(result))
    return result.exit_code


if __name__ == "__main__":
    raise SystemExit(main())
