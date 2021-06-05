#!/usr/bin/env python3
import os
import logging
import pynetbox
import ipaddress
import json

logging.basicConfig(level=logging.DEBUG)
log = logging.getLogger()

netbox = pynetbox.api(
    'https://netbox.generalprogramming.org',
    token=os.environ['NETBOX_KEY'],
)

virtual_interfaces = {}
dcim_interfaces = {}
ips = []
leases = []

def is_internal(address):
    return ipaddress.ip_network(address, strict=False).is_private

def generate_lease(lease_name, hostname: str, mac: str, ip: str):
    return {
        "host": lease_name,
        "hostname": hostname,
        "mac": mac,
        "ip": ip,
    }

def populate_dict(getter, resultdict):
    for x in getter.all():
        resultdict[x.id] = x

if __name__ == "__main__":
    # Populate the virtual / physical interfaces dicts.
    populate_dict(netbox.virtualization.interfaces, virtual_interfaces)
    populate_dict(netbox.dcim.interfaces, dcim_interfaces)

    # Iterate through all IPs that are IPv4.
    for ip in netbox.ipam.ip_addresses.filter(family=4):
        # Ignore IPs that do not have interfaces asscoiated with them.
        if ip.assigned_object_type not in ("dcim.interface", "virtualization.vminterface"):
            log.warning(f"{ip.address} missing interface")
            continue

        # Ignore interfaces that do not have MACs.
        if ip.assigned_object_type == "dcim.interface":
            iface = dcim_interfaces[ip.assigned_object.id]
        elif ip.assigned_object_type == "virtualization.vminterface":
            iface = virtual_interfaces[ip.assigned_object.id]

        # Do not use IPs that do not have MAC addresses.
        if not iface.mac_address:
            log.warning(f"{ip.address} missing MAC")
            continue

        ips.append(ip)

    for ip in ips:
        if ip.assigned_object_type == "virtualization.vminterface":
            interface = virtual_interfaces[ip.assigned_object.id]
            device_name = interface.virtual_machine.name
        else:
            interface = dcim_interfaces[ip.assigned_object.id]
            device_name = interface.device.name


        device_name = device_name.replace(" ", "_").replace(":","").replace("_-_", "_").replace("/", "_")
        interface_name = interface.name.replace(" ", "_").replace(":","").replace("_-_", "_").replace("/", "_")
        hostname = f"{device_name}-{interface_name}".lower()
        address = ip.address.split("/")[0]

        leases.append(generate_lease(
            lease_name=hostname,
            hostname=device_name,
            mac=interface.mac_address,
            ip=address
        ))

    print(json.dumps(leases))
