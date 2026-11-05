import contextlib
import io
from pathlib import Path
import tempfile
import threading
import unittest
from unittest.mock import MagicMock, call, patch

from scapy.all import ARP, Dot1Q, Ether, IP

import observe_ip


def request(source="192.0.2.1", vlan=25, **fields):
    frame = Ether(dst="ff:ff:ff:ff:ff:ff")
    if vlan:
        frame /= Dot1Q(vlan=vlan)
    fields.setdefault("pdst", source)
    return Ether(bytes(frame / ARP(psrc=source, **fields)))


class ObserveTests(unittest.TestCase):
    def test_untagged_startup_records_local_ip_and_scans_after_capture_ready(self):
        for existing in (False, True):
            with self.subTest(existing=existing), tempfile.TemporaryDirectory() as directory:
                path = Path(directory) / "ips.txt"
                if existing:
                    path.write_text("10.20.30.40\n")

                def capture(**kwargs):
                    self.assertEqual(path.read_text(), "10.20.30.40\n")
                    scanner_type.return_value.pending.put.assert_not_called()
                    kwargs["started_callback"]()
                    scanner_type.return_value.pending.put.assert_called_once_with("10.20.30.40")
                    kwargs["prn"](request("10.20.30.41", vlan=0))

                with patch.object(observe_ip, "get_if_addr", return_value="10.20.30.40"), \
                        patch.object(observe_ip, "sniff", side_effect=capture), \
                        patch.object(observe_ip, "SubnetScanner") as scanner_type, \
                        contextlib.redirect_stdout(io.StringIO()), \
                        contextlib.redirect_stderr(io.StringIO()):
                    scanner_type.return_value.mac = "02:00:00:00:00:99"
                    observe_ip.observe("eth0", 0, path)
                    self.assertEqual(scanner_type.return_value.pending.put.call_args_list,
                                     [call("10.20.30.40"), call("10.20.30.41")])
                self.assertEqual(path.read_text(), "10.20.30.40\n10.20.30.41\n")

    def test_startup_without_local_seed(self):
        for vlan in (0, 25):
            with self.subTest(vlan=vlan), tempfile.TemporaryDirectory() as directory:
                path = Path(directory) / "ips.txt"
                with patch.object(observe_ip, "get_if_addr", return_value="0.0.0.0") as get_addr, \
                        patch.object(observe_ip, "sniff") as capture, \
                        patch.object(observe_ip, "SubnetScanner") as scanner_type, \
                        contextlib.redirect_stdout(io.StringIO()), \
                        contextlib.redirect_stderr(io.StringIO()):
                    capture.side_effect = lambda **kwargs: kwargs["started_callback"]()
                    observe_ip.observe("eth0", vlan, path)
                    scanner_type.return_value.pending.put.assert_not_called()
                    self.assertEqual(get_addr.call_count, int(vlan == 0))
                self.assertEqual(path.read_text(), "")

    def test_packet_selection(self):
        for vlan in (0, 25):
            with self.subTest(vlan=vlan):
                self.assertEqual(observe_ip.arp_source(request(vlan=vlan), vlan),
                                 "192.0.2.1")
                self.assertEqual(observe_ip.arp_source(
                    request(source="0.0.0.0", vlan=vlan), vlan), "0.0.0.0")
                for op in (1, 2):
                    for destination in ("ff:ff:ff:ff:ff:ff", "02:00:00:00:00:01"):
                        packet = request(vlan=vlan, op=op)
                        packet.dst = destination
                        self.assertEqual(observe_ip.arp_source(packet, vlan), "192.0.2.1")
                self.assertIsNone(observe_ip.arp_source(request(vlan=vlan, op=3), vlan))
        for packet in (request(vlan=0), request(vlan=26),
                       Ether()/Dot1Q(vlan=25)/Dot1Q(vlan=26)/ARP(),
                       Ether()/IP(), request(hwtype=2)):
            self.assertIsNone(observe_ip.arp_source(packet, 25))
        self.assertIsNone(observe_ip.arp_source(request(), 0))

    def test_persistent_deduplication_and_immediate_write(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "ips.txt"
            # Existing files need not have a trailing newline.
            path.write_text("192.0.2.1", encoding="utf-8")

            def capture(**kwargs):
                self.assertFalse(kwargs["store"])
                self.assertEqual(kwargs["filter"], "arp or (vlan and arp)")
                reply = request("192.0.2.2", op=2)
                reply.dst = "02:00:00:00:00:99"
                for packet in (request(), reply,
                               request("192.0.2.2"), request("192.0.2.3", vlan=26),
                               request("192.0.2.3", vlan=26, op=2)):
                    kwargs["prn"](packet)
                self.assertEqual(path.read_text(), "192.0.2.1\n192.0.2.2\n")

            with patch.object(observe_ip, "sniff", side_effect=capture), \
                    patch.object(observe_ip, "SubnetScanner") as scanner_type, \
                    contextlib.redirect_stdout(io.StringIO()), \
                    contextlib.redirect_stderr(io.StringIO()):
                scanner_type.return_value.mac = "02:00:00:00:00:99"
                observe_ip.observe("eth0", 25, path)
                observe_ip.observe("eth0", 25, path)
                # A new IP triggers a scan, even in an already observed subnet.
                scanner_type.return_value.pending.put.assert_called_once_with("192.0.2.2")
            self.assertEqual(path.read_text(), "192.0.2.1\n192.0.2.2\n")

    def test_scan_targets_and_vlan(self):
        for vlan in (0, 25):
            sender = MagicMock()
            with contextlib.redirect_stderr(io.StringIO()):
                observe_ip.scan_subnet(sender, "02:00:00:00:00:99", vlan,
                                       "0.0.0.0", "10.20.30.42", 0,
                                       threading.Event())
            packets = [Ether(bytes(item.args[0])) for item in sender.send.call_args_list]
            self.assertEqual([p[ARP].pdst for p in packets],
                             [f"10.20.30.{i}" for i in range(1, 255)])
            for packet in packets:
                self.assertEqual(packet.dst, "ff:ff:ff:ff:ff:ff")
                self.assertEqual(packet[ARP].op, 1)
                self.assertEqual(packet[ARP].psrc, "0.0.0.0")
                self.assertEqual(Dot1Q in packet, bool(vlan))
                if vlan:
                    self.assertEqual(packet[Dot1Q].vlan, vlan)

    def test_only_sources_of_requests_and_replies_are_recorded_and_scanned(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "ips.txt"
            path.write_text("10.0.1.1\n")

            def capture(**kwargs):
                for op, target in ((1, "10.0.2.2"), (2, "10.0.3.3"),
                                   (2, "10.0.2.2"), (1, "10.0.1.1")):
                    packet = request("10.0.1.1", op=op, pdst=target)
                    packet.dst = "02:00:00:00:00:99"
                    kwargs["prn"](packet)
                # Targets alone must neither be recorded nor trigger scans.
                self.assertEqual(path.read_text(), "10.0.1.1\n")
                scanner_type.return_value.pending.put.assert_not_called()
                # These addresses become new only when observed as sources.
                kwargs["prn"](request("10.0.2.2", pdst="10.0.3.3"))
                kwargs["prn"](request("10.0.3.3", op=2, pdst="10.0.2.2"))

            with patch.object(observe_ip, "sniff", side_effect=capture), \
                    patch.object(observe_ip, "SubnetScanner") as scanner_type, \
                    contextlib.redirect_stdout(io.StringIO()), \
                    contextlib.redirect_stderr(io.StringIO()):
                scanner_type.return_value.mac = "02:00:00:00:00:99"
                observe_ip.observe("eth0", 25, path)
                self.assertEqual(scanner_type.return_value.pending.put.call_args_list,
                                 [call("10.0.2.2"), call("10.0.3.3")])
            self.assertEqual(path.read_text(), "10.0.1.1\n10.0.2.2\n10.0.3.3\n")

    def test_own_probes_and_unspecified_source_do_not_trigger_scans(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "ips.txt"
            own = request("198.51.100.9", pdst="198.51.100.20")
            own.src = "02:00:00:00:00:99"

            def capture(**kwargs):
                for packet in (own, request("0.0.0.0"),
                               request("0.0.0.0", pdst="10.0.1.2"),
                               request("10.0.1.3", op=2, pdst="0.0.0.0"), request("10.0.1.2"),
                               request("10.0.1.3"), request("10.0.1.2")):
                    kwargs["prn"](packet)

            with patch.object(observe_ip, "sniff", side_effect=capture), \
                    patch.object(observe_ip, "SubnetScanner") as scanner_type, \
                    contextlib.redirect_stdout(io.StringIO()), \
                    contextlib.redirect_stderr(io.StringIO()):
                scanner_type.return_value.mac = own.src
                observe_ip.observe("eth0", 25, path)
                self.assertEqual(scanner_type.return_value.pending.put.call_args_list,
                                 [call("10.0.1.3"), call("10.0.1.2")])
                scanner_type.return_value.close.assert_called_once()
            self.assertEqual(path.read_text(), "10.0.1.3\n10.0.1.2\n")


if __name__ == "__main__":
    unittest.main()
