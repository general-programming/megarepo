from pyinfra import host
from pyinfra.operations import apt, server

holepunch_url = host.data.holepunch_url
holepunch_key = host.data.holepunch_key

holepunch_command = f"curl -L -X POST '{holepunch_url}' -H 'Content-Type: application/json' --data-raw '{{\"key\": \"{holepunch_key}\"}}'"

apt.packages(
    name="Install Curl.",
    packages=[
        "curl",
    ],
    update=True,
)

server.shell(
    name="Punch hole.",
    commands=holepunch_command,
)

server.crontab(
    name="Create holepuncher crontab",
    user="nobody",
    minute="*/60",
    cron_name="vault_holepunch",
    command=holepunch_command,
)
