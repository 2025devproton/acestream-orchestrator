import subprocess
import unittest
from unittest.mock import Mock, patch

from app import start


class SupervisorTests(unittest.TestCase):
    def test_critical_process_exit_is_failure_even_when_zero(self):
        for failing in ("go", "redis"):
            redis = Mock(returncode=0 if failing == "redis" else None)
            go = Mock(returncode=0 if failing == "go" else None)
            redis.poll.return_value = redis.returncode
            go.poll.return_value = go.returncode
            with patch.object(start, "start_redis", return_value=redis), \
                 patch.object(start, "start_go_acestream", return_value=go), \
                 patch.object(start, "start_proton_sidecar", return_value=None), \
                 patch.object(start.signal, "signal"), \
                 patch.object(start.time, "sleep"), \
                 patch.object(start, "_procs", [redis, go]):
                with self.assertRaises(SystemExit) as raised:
                    start.main()
                self.assertEqual(raised.exception.code, 1)
                redis.terminate.assert_called_once()
                go.terminate.assert_called_once()

    def test_signal_shutdown_is_clean_and_kills_unresponsive_child(self):
        child = Mock()
        child.wait.side_effect = [subprocess.TimeoutExpired("child", 40), 0]
        with patch.object(start, "_procs", [child]):
            with self.assertRaises(SystemExit) as raised:
                start._stop_all()
        self.assertEqual(raised.exception.code, 0)
        child.kill.assert_called_once()

    def test_redis_is_tracked_in_foreground(self):
        child = Mock()
        with patch.object(start.subprocess, "Popen", return_value=child) as spawn, \
             patch.object(start.subprocess, "run", return_value=Mock(returncode=0)), \
             patch.object(start, "_procs", []):
            self.assertIs(start.start_redis(), child)
            self.assertIn(child, start._procs)
            args = spawn.call_args.args[0]
            self.assertEqual(args[args.index("--daemonize") + 1], "no")


if __name__ == "__main__":
    unittest.main()
