#!/usr/bin/env python3
"""Configure Windows addresses through QGA using the matching netsh family."""
import ipaddress
import base64
import json
import os
import subprocess
import sys
import time


def guest_netsh(vm, args, allow_missing=False):
    request = {"execute": "guest-exec", "arguments": {
        "path": r"C:\Windows\System32\netsh.exe", "arg": ["interface"] + args,
        "capture-output": True}}
    result = subprocess.run(["virsh", "qemu-agent-command", vm, json.dumps(request)], check=True, text=True, capture_output=True)
    response = json.loads(result.stdout)
    if "error" in response:
        raise RuntimeError(response["error"])
    pid = response["return"]["pid"]
    deadline = time.monotonic() + 30
    while time.monotonic() < deadline:
        status = subprocess.run(["virsh", "qemu-agent-command", vm, json.dumps({
            "execute": "guest-exec-status", "arguments": {"pid": pid}})], check=True, text=True, capture_output=True)
        response = json.loads(status.stdout)
        if "error" in response:
            raise RuntimeError(response["error"])
        if response["return"].get("exited"):
            if response["return"].get("exitcode", 0) and not allow_missing:
                details = response["return"]
                output = base64.b64decode(details.get("err-data") or details.get("out-data") or "").decode(errors="replace")
                raise RuntimeError(f"Windows netsh failed: {args}: {output}")
            return response["return"]
        time.sleep(0.2)
    raise TimeoutError("Windows network configuration did not finish within 30 seconds")


def configure(vm, primary, gateway, extras, dns=""):
    for index, cidr in enumerate(([primary] if primary else []) + extras):
        iface = ipaddress.ip_interface(cidr)
        family = f"ipv{iface.version}"
        is_primary = bool(primary) and index == 0
        if iface.version == 6:
            # Only 'add address' accepts a prefix length in netsh IPv6.
            guest_netsh(vm, [family, "delete", "address", "interface=eth0", f"address={iface.ip}"], allow_missing=True)
            args = [family, "add", "address",
                    "interface=eth0", f"address={iface}", "store=persistent"]
        else:
            args = [family, "set" if is_primary else "add", "address",
                    "name=eth0", f"addr={iface.ip}", f"mask={iface.netmask}"]
            if is_primary:
                args.append("source=static")
                args.append(f"gateway={gateway}")
        guest_netsh(vm, args)
        if is_primary and iface.version == 6 and gateway:
            guest_netsh(vm, [family, "delete", "route", "prefix=::/0", "interface=eth0", f"nexthop={gateway}"], allow_missing=True)
            guest_netsh(vm, [family, "add", "route", "prefix=::/0",
                            "interface=eth0", f"nexthop={gateway}", "store=persistent"])
        if is_primary and dns:
            guest_netsh(vm, [f"ipv{ipaddress.ip_address(dns).version}", "set", "dnsservers",
                            "name=eth0", "source=static", f"address={dns}", "validate=no"])


def remove(vm, addresses):
    for cidr in addresses:
        iface = ipaddress.ip_interface(cidr)
        args = [f"ipv{iface.version}", "delete", "address",
                "interface=eth0" if iface.version == 6 else "name=eth0",
                f"address={iface.ip}" if iface.version == 6 else f"addr={iface.ip}"]
        guest_netsh(vm, args, allow_missing=True)


def primary_cidr(network):
    address, mask = network["ip_address"], network["netmask"]
    if ":" in address and ":" in str(mask):
        mask = bin(int(ipaddress.IPv6Address(mask))).count("1")
    return f"{address}/{mask}"


if __name__ == "__main__":
    vm = sys.argv[1]
    if sys.argv[2] == "--remove":
        remove(vm, json.load(sys.stdin) or [])
    elif sys.argv[2] == "--metadata":
        data = json.load(sys.stdin)
        primary = next(n for n in data["networks"] if n["id"] == "network0" and n.get("routes"))
        dns = data.get("dns") or ""
        if primary["type"] == "ipv6":
            if ":" not in dns:
                dns = os.environ.get("dns6_server", "")
        else:
            dns = dns or os.environ.get("dns_server", "")
        extras = [primary_cidr(n) for n in data["networks"] if n["id"] == "network0" and not n.get("routes")]
        configure(vm, primary_cidr(primary), primary["routes"][0]["gateway"], extras, dns)
    else:
        configure(vm, sys.argv[2], sys.argv[3], json.loads(sys.argv[4]) or [])
