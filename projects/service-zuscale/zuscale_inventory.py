import asyncio
import os

from zuscale.providers import ALL_CLOUDS

zuscale_hosts = []


async def get_hosts():
    for provider, _cloud in ALL_CLOUDS.items():
        # Skip providers that are not the provider specified in environ.
        if "PROVIDER" in os.environ and provider != os.environ["PROVIDER"]:
            continue

        # Variables for all servers.
        ssh_user = "root"

        # ec2 specific bs
        if provider == "ec2":
            ssh_user = "ec2-user"

        cloud = _cloud()

        servers = await cloud.list_servers()

        for server in servers:
            # Ignore servers without IPs or tags.
            if not server.ip4 or not server.server_tags:
                continue

            # Add servers if they have a special tag on them.
            # XXX Find a way to make this less fixed.
            if "project_erin_archiveteam" in server.server_tags or "PYINFRA_ALL" in os.environ:
                zuscale_hosts.append(
                    (server.ip4, {
                        "ssh_user": ssh_user,
                        "provider": cloud.NAME,
                    })
                )

        await cloud.cleanup()


def get_static_hosts():
    if not os.path.exists("./static_hosts.txt"):
        return

    with open("./static_hosts.txt", "r") as f:
        for host in f.readlines():
            host = host.strip()
            if not host:
                continue
            zuscale_hosts.append(
                (host, {
                    "ssh_user": "root",
                    "provider": "static",
                })
            )


# Main area
asyncio.run(get_hosts())  # Add dynamic hosts first.

# Add static hosts if the provider is undefined or is static.
if os.environ.get("PROVIDER", "static") == "static":
    get_static_hosts()  # Add static hosts next.

print(zuscale_hosts)
