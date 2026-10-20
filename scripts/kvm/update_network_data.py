#!/usr/bin/env python3
"""Update config-drive addresses while preserving the primary route family."""
import ipaddress
import json
import sys


def address_config(cidr):
    iface = ipaddress.ip_interface(cidr)
    return {"type": f"ipv{iface.version}", "ip_address": str(iface.ip),
            "netmask": str(iface.netmask)}


def update(data, primary_ip, gateway, extras):
    networks = data["networks"]
    primary = next(n for n in networks if n["id"] == "network0" and n.get("routes"))
    if primary_ip:
        primary.update(address_config(primary_ip))
        default = "::" if ipaddress.ip_interface(primary_ip).version == 6 else "0.0.0.0"
        primary["routes"] = [{"network": default, "netmask": default,
                              "gateway": gateway.split("/")[0]}]
    new_networks = [primary]
    for cidr in extras:
        network = address_config(cidr)
        network.update(link=primary["link"], id="network0")
        new_networks.append(network)
    data["networks"] = new_networks + [n for n in networks if n["id"] != "network0"]
    return data


if __name__ == "__main__":
    json.dump(update(json.load(sys.stdin), sys.argv[1], sys.argv[2],
                     json.loads(sys.argv[3] or "[]") or []), sys.stdout)
