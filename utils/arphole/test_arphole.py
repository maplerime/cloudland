import pathlib
import subprocess
import sys
import textwrap
import unittest
from unittest.mock import patch

import arphole


class WorkerFailureTests(unittest.TestCase):
    def test_worker_failure_or_return_exits_entire_process(self):
        # Real child processes verify that sleeping sibling threads do not
        # keep the service alive. No capture or send sockets are opened.
        for role, exception in [
            ("gc", "RuntimeError"),
            ("sender", "SystemExit"),
            ("sweeper", "RuntimeError"),
            ("request", "RuntimeError"),
            ("reply", "RuntimeError"),
            ("reply", None),
        ]:
            with self.subTest(role=role, exception=exception):
                script = textwrap.dedent(f"""
                    import threading
                    import arphole

                    hole = arphole.ArpHole([])
                    hole.ifaces = ["fake0"]
                    blocker = threading.Event()
                    def wait(*args):
                        blocker.wait()
                    def fail(*args):
                        {f'raise {exception}("injected failure")' if exception else 'return'}
                    hole._gc_loop = fail if {role!r} == "gc" else wait
                    hole._sender_loop = fail if {role!r} == "sender" else wait
                    hole._sweep_loop = fail if {role!r} == "sweeper" else wait
                    def sniff(iface, role):
                        if role == {role!r}:
                            fail()
                        else:
                            wait()
                    hole._sniff_iface = sniff
                    hole.run()
                    raise AssertionError("service returned instead of exiting")
                """)
                result = subprocess.run(
                    [sys.executable, "-c", script],
                    cwd=pathlib.Path(__file__).resolve().parent,
                    capture_output=True, text=True, timeout=10,
                )
                self.assertEqual(result.returncode, 1, result.stderr)
                worker = f"sniff-fake0-{role}" if role in ("request", "reply") else role
                self.assertIn(f"worker {worker}", result.stderr)
                self.assertIn("exiting process", result.stderr)

    def test_main_failure_exits_without_retry(self):
        with patch.object(sys, "argv", ["arphole.py", "--iface", "fake0"]), \
                patch.object(arphole, "ArpHole") as hole, \
                patch.object(arphole.signal, "signal"), \
                self.assertLogs("arphole", level="ERROR"):
            hole.return_value.run.side_effect = RuntimeError("startup failure")
            self.assertEqual(arphole.main(), 1)
            hole.return_value.run.assert_called_once()

    def test_sender_continues_after_individual_send_failure(self):
        hole = arphole.ArpHole([])
        task = arphole.ProbeTask("192.0.2.2", "02:00:00:00:00:01", "192.0.2.1", 25)
        key = ("fake0", task.target_ip, task.vlan_id)
        next_task = task._replace(target_ip="192.0.2.3")
        next_key = ("fake0", next_task.target_ip, next_task.vlan_id)
        hole._inflight.add(key)
        hole._inflight.add(next_key)
        hole._work_q.put(("fake0", task.target_ip, task))
        hole._work_q.put(("fake0", next_task.target_ip, next_task))
        hole._work_q.put(None)
        with patch.object(hole, "_send_probes", side_effect=[OSError("send failed"), None]) as send, \
                self.assertLogs("arphole", level="ERROR"):
            hole._sender_loop()
        self.assertEqual(send.call_count, 2)
        self.assertEqual(send.call_args.args, ("fake0", next_task))
        self.assertNotIn(key, hole._probing)
        self.assertNotIn(key, hole._inflight)
        self.assertIn(next_key, hole._probing)
        self.assertEqual(hole._work_q.unfinished_tasks, 0)

    def test_gc_and_sweep_continue_after_iteration_failure(self):
        class StopLoop(BaseException):
            pass

        hole = arphole.ArpHole([])
        for loop, operation in [(hole._gc_loop, "_gc"), (hole._sweep_loop, "_sweep")]:
            with self.subTest(operation=operation), \
                    patch.object(arphole.time, "sleep", side_effect=[None, None, StopLoop()]), \
                    patch.object(hole, operation, side_effect=[RuntimeError("loop failed"), None]) as run, \
                    self.assertLogs("arphole", level="ERROR"), \
                    self.assertRaises(StopLoop):
                loop()
            self.assertEqual(run.call_count, 2)

    def test_capture_failure_is_propagated(self):
        hole = arphole.ArpHole([])
        with patch.object(arphole, "sniff", side_effect=OSError("capture failed")), \
                self.assertLogs("arphole", level="ERROR"), \
                self.assertRaises(OSError):
            hole._sniff_iface("fake0", "reply")

    def test_reclaim_send_failure_does_not_silence_target(self):
        hole = arphole.ArpHole([])
        task = arphole.ProbeTask("192.0.2.2", "02:00:00:00:00:01", "192.0.2.1", 25)
        key = ("fake0", task.target_ip, task.vlan_id)
        with patch.object(hole, "_get_send_socket", side_effect=OSError("send failed")), \
                self.assertLogs("arphole", level="ERROR"):
            hole._do_reclaim("fake0", task)
        self.assertNotIn(key, hole._silent_until)

    def test_sweep_continues_after_individual_reclaim_failure(self):
        hole = arphole.ArpHole([])
        task = arphole.ProbeTask("192.0.2.2", "02:00:00:00:00:01", "192.0.2.1", 25)
        key = ("fake0", task.target_ip, task.vlan_id)
        next_task = task._replace(target_ip="192.0.2.3")
        next_key = ("fake0", next_task.target_ip, next_task.vlan_id)
        hole._probing[key] = (0.0, task)
        hole._probing[next_key] = (0.0, next_task)
        hole._inflight.add(key)
        hole._inflight.add(next_key)
        with patch.object(hole, "_do_reclaim", side_effect=[RuntimeError("reclaim failed"), None]) as reclaim, \
                self.assertLogs("arphole", level="ERROR"):
            hole._sweep()
        self.assertEqual(reclaim.call_count, 2)
        self.assertEqual(reclaim.call_args.args, ("fake0", next_task))
        self.assertNotIn(key, hole._inflight)
        self.assertNotIn(next_key, hole._inflight)


if __name__ == "__main__":
    unittest.main()
