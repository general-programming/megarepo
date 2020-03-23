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

def generate_lease(hostname: str, mac: str, ip: str):
    return {
        "host": hostname,
        "mac": mac,
        "ip": ip,
    }

if __name__ == "__main__":
    for interface in netbox.virtualization.interfaces.all():
        virtual_interfaces[interface.id] = interface

    for interface in netbox.dcim.interfaces.all():
        dcim_interfaces[interface.id] = interface

    for ip in netbox.ipam.ip_addresses.filter(family=4):
        # Filer out the internal interfaces.
        if not is_internal(ip.address):
            continue

        # Ignore IPs that do not have interfaces asscoiated with them.
        if not ip.interface:
            log.warning(f"{ip.address} missing interface")
            continue

        # Ignore interfaces that do not have MACs.
        if ip.interface.device:
            iface = dcim_interfaces[ip.interface.id]
        elif ip.interface.virtual_machine:
            iface = virtual_interfaces[ip.interface.id]

        mac = iface.mac_address

        if not mac:
            log.warning(f"{ip.address} missing MAC")
            continue

        ips.append(ip)

    for ip in ips:
        if ip.interface.virtual_machine:
            interface = virtual_interfaces[ip.interface.id]
            device_name = interface.virtual_machine.name
        else:
            interface = dcim_interfaces[ip.interface.id]
            device_name = interface.device.name
            

        device_name = device_name.replace(" ", "_").replace(":","")
        hostname = f"{device_name}-{interface.name}".lower()
        address = ip.address.split("/")[0]

        leases.append(generate_lease(hostname, interface.mac_address, address))

    print(json.dumps(leases))
