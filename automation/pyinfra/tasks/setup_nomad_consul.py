import ipaddress
import json
import os

from pyinfra import host
from pyinfra.facts.hardware import NetworkDevices
from pyinfra.operations import files, server, systemd

CLUSTER_CIDR = ipaddress.ip_network("10.101.0.0/24")
in_cluster_cidr = False

# Get the CIDR for the cluster network.
for dev, dev_config in host.get_fact(NetworkDevices).items():
    # Easiest: Check if the device is in netmaker.
    if dev == "nm-netmaker":
        in_cluster_cidr = True
        break

    # Check if the device is in the cluster CIDR if that fails..
    if dev_config["ipv4"]:
        dev_v4_addr = ipaddress.IPv4Address(dev_config["ipv4"]["address"])

        # Check if the CIDR is 10.101.0.0/24
        if dev_v4_addr in CLUSTER_CIDR:
            in_cluster_cidr = True
            break

files.template(
    name="Create Consul config.",
    src="templates/consul/node.j2",
    dest="/etc/consul.d/consul.hcl",
    mode="644",
    consul_datacenter=host.data.consul_datacenter,
    consul_servers=json.dumps(host.data.consul_servers),
    in_cluster_cidr=in_cluster_cidr,
)

files.put(
    name="Consul node_exporter template.",
    src="common/consul_configs/service_node_exporter.json",
    dest="/etc/consul.d/service_node_exporter.json",
    mode="644",
)

server.service(
    name="Restart Consul.",
    service="consul",
    running=True,
    restarted=True,
    enabled=True,
)
