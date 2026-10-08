import unittest
import contextlib
import io
import socket
import struct
import sys
from unittest.mock import patch

from scapy.all import ARP, Dot1Q, Ether

import arpdetect


class TargetTests(unittest.TestCase):
    def test_mixed_specs_and_deduplication(self):
        self.assertEqual(
            arpdetect.parse_targets(["10.0.0.5,10.0.0.4/30", "10.0.0.6-10.0.0.8"]),
            [f"10.0.0.{i}" for i in range(5, 9)],
        )

    def test_small_networks(self):
        self.assertEqual(arpdetect.parse_targets(["10.0.0.0/31", "10.0.0.2/32"]),
                         ["10.0.0.0", "10.0.0.1", "10.0.0.2"])

    def test_invalid_specs(self):
        for spec in ["::1", "10.0.0.999", "10.0.0.2-10.0.0.1", "10.0.0.1,", ""]:
            with self.subTest(spec=spec), self.assertRaises(ValueError):
                arpdetect.parse_targets([spec])

    def test_limit_before_expansion(self):
        with self.assertRaisesRegex(ValueError, "exceed"):
            arpdetect.parse_targets(["0.0.0.0/0"])


class ProbeTests(unittest.TestCase):
    mac = "02:00:00:00:00:01"
    peer = "02:00:00:00:00:02"
    source = "10.0.0.100"

    def test_wire_format(self):
        for vlan in [0, 25]:
            with self.subTest(vlan=vlan):
                packet = Ether(bytes(arpdetect.make_probe(self.mac, self.source, "10.0.0.5", vlan)))
                self.assertEqual(Dot1Q in packet, bool(vlan))
                self.assertEqual(packet[ARP].op, 1)
                self.assertEqual(packet[ARP].pdst, "10.0.0.5")
                self.assertEqual(packet[ARP].psrc, self.source)
                self.assertEqual(packet.dst, "ff:ff:ff:ff:ff:ff")
                if vlan:
                    self.assertEqual(packet[Dot1Q].vlan, vlan)

    def test_concurrent_collection_and_cleanup(self):
        for vlan in [0, 25]:
            with self.subTest(vlan=vlan):
                self.run_collection(vlan)

    def test_target_broadcast_requests_are_ignored(self):
        for vlan in [0, 25]:
            with self.subTest(vlan=vlan):
                self.run_collection(vlan, "request")

    def test_gratuitous_replies(self):
        self.run_collection(25, "announcement")

    def test_replies_to_another_destination_are_accepted(self):
        self.run_collection(25, "foreign_destination")

    def test_debug_reports_rejection_and_acceptance(self):
        output = self.run_collection(25, debug=True)
        self.assertIn("VLAN mismatch", output)
        self.assertIn("received untagged", output)
        self.assertIn("sender MAC equals interface MAC", output)
        self.assertIn("accept ", output)
        self.assertIn("ARP frames from targets", output)

    def run_collection(self, vlan, kind="reply", debug=False):
        state = {"sent": [], "closed": [], "sniffer": None}
        case = self

        def reply(target, tag=vlan, mac=self.peer, **fields):
            packet = Ether(src=mac, dst=self.mac)
            if tag:
                packet /= Dot1Q(vlan=tag)
            values = dict(op=2, hwsrc=mac, psrc=target, hwdst=self.mac, pdst=self.source)
            values.update(fields)
            return Ether(bytes(packet / ARP(**values)))

        class Sniffer:
            def __init__(self, **kwargs):
                self.kwargs = kwargs
                self.running = False
                state["sniffer"] = self

            def start(self):
                self.running = True
                self.kwargs["started_callback"]()

            def stop(self):
                self.running = False

        class Socket:
            def __init__(self, role):
                self.role = role

            def close(self):
                state["closed"].append(self.role)

            def send(self, packet):
                case.assertTrue(state["sniffer"].running)
                state["sent"].append(packet[ARP].pdst)
                callback = state["sniffer"].kwargs["prn"]
                callback(reply("10.0.0.5", tag=99, mac="02:00:00:00:00:fe"))
                if vlan:
                    callback(reply("10.0.0.5", tag=0))
                callback(reply("10.0.0.5", op=3, mac="02:00:00:00:00:fe"))
                callback(reply("10.0.0.5", mac=case.mac))
                callback(reply("10.0.0.5", mac="00:00:00:00:00:00"))
                callback(reply("10.0.0.5", mac="01:00:00:00:00:01"))
                callback(reply("10.0.0.5", hwtype=2, mac="02:00:00:00:00:fe"))
                callback(reply("10.0.0.9"))
                callback(reply("10.0.0.9", op=1, pdst="10.0.0.6"))

        def wait(seconds):
            case.assertEqual(state["sent"], ["10.0.0.5", "10.0.0.6"] * 2)
            callback = state["sniffer"].kwargs["prn"]
            fields = {}
            if kind == "request":
                fields = dict(op=1, hwdst="00:00:00:00:00:00", pdst="10.0.0.254")
            elif kind == "announcement":
                fields = dict(hwdst="ff:ff:ff:ff:ff:ff", pdst="10.0.0.5")
            for peer in [case.peer, case.peer, "02:00:00:00:00:03"]:
                packet = reply("10.0.0.5", mac=peer, **fields)
                if kind != "reply":
                    packet[Ether].dst = "ff:ff:ff:ff:ff:ff"
                if kind == "foreign_destination":
                    packet[Ether].dst = "ce:b0:11:44:ff:e1"
                    packet[ARP].hwdst = "ce:b0:11:44:ff:e1"
                    packet[ARP].pdst = "192.0.2.100"
                callback(packet)

        debug_output = io.StringIO()
        with patch.object(arpdetect, "get_if_hwaddr", return_value=self.mac), \
                patch.object(arpdetect.conf, "L2listen", return_value=Socket("receiver")) as listen, \
                patch.object(arpdetect.conf, "L2socket", return_value=Socket("sender")) as open_sender, \
                patch.object(arpdetect, "AsyncSniffer", Sniffer), \
                patch.object(arpdetect.time, "sleep", side_effect=wait), \
                contextlib.redirect_stderr(debug_output):
            result = arpdetect.detect("fake0", vlan, ["10.0.0.5", "10.0.0.6"], self.source,
                                      count=2, debug=debug)
            listen.assert_called_once_with(iface="fake0", filter=arpdetect.REPLY_FILTER)
            open_sender.assert_called_once_with(iface="fake0")
        expected = {} if kind == "request" else {"10.0.0.5": {self.peer, "02:00:00:00:00:03"}}
        self.assertEqual(result, expected)
        self.assertEqual(state["closed"], ["sender", "receiver"])
        self.assertFalse(state["sniffer"].running)
        return debug_output.getvalue()

    def test_sender_open_failure_closes_receiver(self):
        from unittest.mock import Mock
        receiver = Mock()
        with patch.object(arpdetect, "get_if_hwaddr", return_value=self.mac), \
                patch.object(arpdetect.conf, "L2listen", return_value=receiver), \
                patch.object(arpdetect.conf, "L2socket", side_effect=PermissionError("raw socket")):
            with self.assertRaises(PermissionError):
                arpdetect.detect("fake0", 0, ["10.0.0.5"], self.source)
        receiver.close.assert_called_once()


class CaptureTests(unittest.TestCase):
    @unittest.skipUnless(sys.platform.startswith("linux"), "Linux socket filter")
    def test_kernel_filter_accepts_only_arp_replies(self):
        # Exercise the actual kernel BPF on a local datagram socket pair;
        # nothing is transmitted to a network interface.
        from scapy.arch.common import compile_filter
        from scapy.libs.structures import sock_fprog
        from scapy.data import SO_ATTACH_FILTER

        program = compile_filter(arpdetect.REPLY_FILTER, linktype=1)
        receiver, sender = socket.socketpair(socket.AF_UNIX, socket.SOCK_DGRAM)
        try:
            receiver.setsockopt(socket.SOL_SOCKET, SO_ATTACH_FILTER,
                                sock_fprog(program.bf_len, program.bf_insns))
            receiver.settimeout(0.05)
            ether = Ether(src="02:00:00:00:00:02", dst="02:00:00:00:00:01")

            def arp(op):
                return ARP(op=op, hwsrc=ether.src, hwdst=ether.dst,
                           psrc="192.0.2.2", pdst="192.0.2.1")

            frames = [
                (ether / arp(2), True),
                (ether / Dot1Q(vlan=25) / arp(2), True),
                (ether / arp(1), False),
                (ether / Dot1Q(vlan=25) / arp(1), False),
                (Ether(src=ether.src, dst=ether.dst, type=0x0800) / bytes(60), False),
                (ether / Dot1Q(vlan=25, type=0x0800) / bytes(60), False),
            ]
            for frame, accepted in frames:
                with self.subTest(frame=frame.summary()):
                    wire = bytes(frame)
                    sender.send(wire)
                    if accepted:
                        self.assertEqual(receiver.recv(65535), wire)
                    else:
                        with self.assertRaises(socket.timeout):
                            receiver.recv(65535)
        finally:
            sender.close()
            receiver.close()

    def test_packet_socket_drop_stats(self):
        from unittest.mock import Mock
        receiver = Mock()
        receiver.ins.family = socket.AF_PACKET
        receiver.ins.getsockopt.return_value = struct.pack("=II", 14000, 3000)
        self.assertEqual(arpdetect.packet_socket_stats(receiver), (14000, 3000))
        receiver.ins.getsockopt.assert_called_once_with(
            getattr(socket, "SOL_PACKET", 263), 6, 8)

    def test_packet_socket_stats_unavailable(self):
        from unittest.mock import Mock
        receiver = Mock()
        receiver.ins.family = socket.AF_PACKET
        receiver.ins.getsockopt.side_effect = OSError("unsupported")
        self.assertIsNone(arpdetect.packet_socket_stats(receiver))


class SourceTests(unittest.TestCase):
    def test_explicit_zero_source_is_preserved(self):
        with patch.object(arpdetect, "detect", return_value={}) as detect, \
                contextlib.redirect_stdout(io.StringIO()), \
                contextlib.redirect_stderr(io.StringIO()):
            self.assertEqual(arpdetect.main(["ens5", "25", "198.2.215.39",
                                            "--source-ip", "0.0.0.0"]), 0)
        self.assertEqual(detect.call_args.args[3], "0.0.0.0")

    def test_default_source_is_documentation_address(self):
        with patch.object(arpdetect, "detect", return_value={}) as detect, \
                contextlib.redirect_stdout(io.StringIO()), \
                contextlib.redirect_stderr(io.StringIO()):
            self.assertEqual(arpdetect.main(["ens5", "25", "198.2.215.39"]), 0)
        self.assertEqual(detect.call_args.args[3], "192.0.2.100")


if __name__ == "__main__":
    unittest.main()
