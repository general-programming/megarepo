from pyinfra.operations import files

files.put(
    name="Install HSM root CA.",
    src="files/root_ca.crt",
    dest="/etc/ssl/certs/General_Programming_Root.pem",
    mode="644",
)
