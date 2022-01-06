import ipaddress
import random

from pyinfra import host
from pyinfra.operations import server, files, systemd

v6_addr = None
v6_device = None

# Find the public IPv6 address of the machine.
for dev, dev_config in host.fact.network_devices.items():
    if dev_config["ipv6"]:
        # Ignore interfaces that have blocks too small to split.
        if dev_config["ipv6"]["mask_bits"] > 96:
            continue

        dev_v6_addr = ipaddress.IPv6Network(f'{dev_config["ipv6"]["address"]}/{dev_config["ipv6"]["mask_bits"]}', strict=False)
        if dev_v6_addr.is_global:
            v6_addr = dev_v6_addr
            v6_device = dev
            break

if v6_addr:
    docker_net_bits = random.getrandbits(v6_addr.max_prefixlen - v6_addr.prefixlen)
    docker_net = ipaddress.IPv6Network(v6_addr.network_address + docker_net_bits).supernet(128 - 96)

    files.template(
        name='Create docker config.',
        src='templates/docker.json.j2',
        dest='/etc/docker/daemon.json',
        docker_network=docker_net,
    )

    files.template(
        name='Create ndppd config.',
        src='templates/ndppd.conf.j2',
        dest='/etc/ndppd.conf',
        docker_network=docker_net,
        ipv6_interface=v6_device
    )

    server.service(
        name="Restart docker.",
        service="docker",
        enabled=True,
        restarted=True,
        running=True,
    )

    # IDK what is going on but ndppd does not restart with server.service?
    # It seems to work with server.shell though.
    # 10/10 "oh that is horrifying, my auto deploy script failed for provisioning ipv6"
    server.shell(
        name='Restart ndppd via the CLI.',
        commands=["systemctl restart ndppd"],
    )

    server.service(
        name="Restart ndppd again.",
        service="ndppd",
        enabled=True,
        restarted=True,
        running=True,
    )
else:
    print("No IPv6 address.")
