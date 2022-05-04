import json
import os
import string
from base64 import decode
from sqlite3 import adapt
from typing import Type

import redis
from pyinfra import host
from pyinfra.facts.server import Command
from pyinfra.operations import apt, files, server

r = redis.Redis(decode_responses=True)

v4_addr = host.get_fact(Command, "curl ifconfig.me")
try:
    files.rsync(
        name="rsync config files",
        src="links/",
        dest="/opt/ircd/conf/servers/",
        flags=["-av"],
    )

    server_links = json.loads(r.hget("pissnet:links", v4_addr))
    for link in server_links:
        files.template(
            name="generate link conf",
            src="templates/pissnet-link.j2",
            dest=f"/opt/ircd/conf/servers/ec2-{link[1]}.conf",
            server_name=link[0],
            server_ip=link[1],
            autoconnect=True,
        )
except TypeError:
    pass
