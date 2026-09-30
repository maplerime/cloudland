import unittest
import contextlib
import io
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

    def run_collection(self, vlan, kind="reply"):
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
                callback(packet)

        with patch.object(arpdetect, "get_if_hwaddr", return_value=self.mac), \
                patch.object(arpdetect.conf, "L2listen", return_value=Socket("receiver")) as listen, \
                patch.object(arpdetect.conf, "L2socket", return_value=Socket("sender")) as open_sender, \
                patch.object(arpdetect, "AsyncSniffer", Sniffer), \
                patch.object(arpdetect.time, "sleep", side_effect=wait):
            result = arpdetect.detect("fake0", vlan, ["10.0.0.5", "10.0.0.6"], self.source, count=2)
            listen.assert_called_once_with(iface="fake0")
            open_sender.assert_called_once_with(iface="fake0")
        expected = {} if kind == "request" else {"10.0.0.5": {self.peer, "02:00:00:00:00:03"}}
        self.assertEqual(result, expected)
        self.assertEqual(state["closed"], ["sender", "receiver"])
        self.assertFalse(state["sniffer"].running)

    def test_sender_open_failure_closes_receiver(self):
        from unittest.mock import Mock
        receiver = Mock()
        with patch.object(arpdetect, "get_if_hwaddr", return_value=self.mac), \
                patch.object(arpdetect.conf, "L2listen", return_value=receiver), \
                patch.object(arpdetect.conf, "L2socket", side_effect=PermissionError("raw socket")):
            with self.assertRaises(PermissionError):
                arpdetect.detect("fake0", 0, ["10.0.0.5"], self.source)
        receiver.close.assert_called_once()


class SourceTests(unittest.TestCase):
    def test_explicit_zero_source_is_preserved(self):
        with patch.object(arpdetect, "get_if_addr") as get_addr, \
                patch.object(arpdetect, "detect", return_value={}) as detect, \
                contextlib.redirect_stdout(io.StringIO()), \
                contextlib.redirect_stderr(io.StringIO()):
            self.assertEqual(arpdetect.main(["ens5", "25", "198.2.215.39",
                                            "--source-ip", "0.0.0.0"]), 0)
        get_addr.assert_not_called()
        self.assertEqual(detect.call_args.args[3], "0.0.0.0")

    def test_unassigned_interface_uses_fallback(self):
        with patch.object(arpdetect, "get_if_addr", return_value="0.0.0.0"), \
                patch.object(arpdetect, "detect", return_value={}) as detect, \
                contextlib.redirect_stdout(io.StringIO()), \
                contextlib.redirect_stderr(io.StringIO()):
            self.assertEqual(arpdetect.main(["ens5", "25", "198.2.215.39"]), 0)
        self.assertEqual(detect.call_args.args[3], "192.0.2.100")


if __name__ == "__main__":
    unittest.main()
