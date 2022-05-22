from pyinfra.operations import files, server

server.shell(
    name="update authorized_keys with keys from instance_keys",
    commands="scw-fetch-ssh-keys --upgrade",
)
