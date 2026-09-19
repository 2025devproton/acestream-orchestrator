import copy
import io
import unittest
from unittest.mock import patch
from urllib.error import URLError

from app import readiness


def status():
    return {
        "status": "healthy",
        "engines": {"total": 2, "healthy": 2, "unhealthy": 0, "draining": 0},
        "capacity": {"total": 2, "used": 1, "available": 1, "min_replicas": 2, "max_replicas": 6},
        "vpn": {"enabled": False, "nodes_total": 0, "healthy": 0},
        "provisioning": {"can_provision": True, "circuit_breaker_state": "closed"},
    }


class ReadinessTests(unittest.TestCase):
    def test_serving_does_not_require_provisioning(self):
        data = status()
        data["provisioning"] = {"can_provision": False, "circuit_breaker_state": "open"}
        self.assertTrue(readiness.evaluate(data).ready)
        self.assertFalse(readiness.evaluate(data, provisioning_only=True).ready)

    def test_minimum_policy_is_explicit(self):
        data = status()
        data["engines"].update(healthy=1, unhealthy=1)
        self.assertTrue(readiness.evaluate(data).ready)
        self.assertFalse(readiness.evaluate(data, require_minimum=True).ready)

    def test_scale_to_zero_can_provision_vpn_lazily(self):
        for vpn in (False, True):
            data = status()
            data["engines"].update(total=0, healthy=0)
            data["capacity"].update(total=0, used=0, available=0, min_replicas=0)
            data["vpn"]["enabled"] = vpn
            result = readiness.evaluate(data)
            self.assertEqual(result.details["mode"], "idle")
            self.assertEqual(result.exit_code, 0)
            data["provisioning"]["can_provision"] = False
            self.assertFalse(readiness.evaluate(data).ready)

    def test_busy_healthy_engines_remain_ready(self):
        data = status()
        data["capacity"].update(used=2, available=0)
        self.assertTrue(readiness.evaluate(data).ready)

    def test_unhealthy_or_vpn_unavailable(self):
        data = status()
        data["vpn"]["enabled"] = True
        self.assertFalse(readiness.evaluate(data).ready)
        data["vpn"].update(nodes_total=1, healthy=1)
        self.assertTrue(readiness.evaluate(data).ready)
        data["engines"].update(healthy=0, unhealthy=2)
        self.assertFalse(readiness.evaluate(data).ready)

    def test_invalid_schema_and_capacity(self):
        base = status()
        cases = [None, {}, {**base, "engines": None}]
        for field, value in (("used", 0), ("total", True), ("available", -1), ("min_replicas", 7)):
            data = copy.deepcopy(base)
            data["capacity"][field] = value
            cases.append(data)
        for data in cases:
            self.assertEqual(readiness.evaluate(data).exit_code, 1)

    def test_transport_and_response_failures(self):
        with patch.object(readiness, "urlopen", side_effect=URLError("secret transport details")):
            result = readiness.probe("http://localhost")
            self.assertEqual(result.exit_code, 1)
            self.assertIn("request failed", result.reason)
            self.assertNotIn("secret", result.reason)
        for raw in (b"not JSON", b"[]", b"x" * (readiness.MAX_RESPONSE_BYTES + 1)):
            response = unittest.mock.MagicMock()
            response.__enter__.return_value = response
            response.status = 200
            response.read.return_value = raw
            with patch.object(readiness, "urlopen", return_value=response):
                self.assertEqual(readiness.probe("http://localhost").exit_code, 1)

    def test_liveness_contract(self):
        for payload, ready in (({}, False), ({"status": "healthy"}, True)):
            with patch.object(readiness, "_get_json", return_value=payload):
                self.assertEqual(readiness.probe("http://localhost", liveness=True).ready, ready)

    def test_invalid_timeouts(self):
        for value in ("0", "-1", "nan", "inf"):
            with patch("sys.stdout", new_callable=io.StringIO):
                self.assertEqual(readiness.main(["--timeout", value]), 1)


if __name__ == "__main__":
    unittest.main()
