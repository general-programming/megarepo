from pyinfra.operations import files, server, systemd

files.put(
    name="Install HSM root CA.",
    src="common/root_ca.crt",
    dest="/etc/ssl/certs/General_Programming_Root.pem",
    mode="644",
)

server.packages(
    name="Install base image packages.",
    packages=[
        "gnupg",
        "software-properties-common",
    ],
)

# Systemd is the world's most OK launch daemon.
files.directory(
    "/etc/systemd/resolved.conf.d",
    present=True,
)

files.put(
    name="Install Consul resolved config.",
    src="files/resolved/consul.conf",
    dest="/etc/systemd/resolved.conf.d/00-consul.conf",
    mode="644",
)

systemd.service(
    name="Restart and resolved",
    service="systemd-resolved.service",
    running=True,
    restarted=True,
    enabled=True,
)
