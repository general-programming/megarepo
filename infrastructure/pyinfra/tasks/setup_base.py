from pyinfra.operations import files, server

files.put(
    name="Install HSM root CA.",
    src="files/root_ca.crt",
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
