from pyinfra import host
from pyinfra.operations import server

holepunch_command = f"curl -L -X POST '{host.data.holepunch_url}' -H 'Content-Type: application/json' --data-raw '{{\"key\": \"{host.data.holepunch_key}\"}}'"

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
