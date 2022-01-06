import os

from base64 import decode
import random
from sqlite3 import adapt
import redis
import string

from pyinfra import host
from pyinfra.operations import server, files, apt

r = redis.Redis(decode_responses=True)

if "BUILD" in os.environ:
    server.shell(
        name="download tar.",
        commands="cd /opt && sudo wget https://erinstore-west2-public.s3-us-west-2.amazonaws.com/ircd.tar.bz2",
    )

    server.shell(
        name="untar",
        commands="cd /opt && sudo tar xfv ircd.tar.bz2"
    )

    server.shell(
        name="fix perms",
        commands="cd /opt && sudo chown -Rv 1000:1000 ircd"
    )

files.rsync(
    name="rsync config files",
    src="files/pissnet/",
    dest="/opt/ircd/conf/",
    flags=['-av', '--delete']
)

sids = r.smembers("sids")

def create_sid():
    while True:
        random_sid = (random.choice(string.digits) + random.choice(string.ascii_letters) + random.choice(string.ascii_letters)).upper()
        if random_sid in sids:
            continue

        return random_sid


sid = create_sid()

files.template(
    name="generate dynamic conf",
    src="templates/pissnet.j2",
    dest="/opt/ircd/conf/dynamic.conf",
    server_name=f"pissbottle-{sid}.us-west-2.compute.amazonaws.com",
    sid=sid
)

v4_addr = host.fact.command("curl ifconfig.me")
server_name = f"pissbottle-{sid}.us-west-2.compute.amazonaws.com"

files.template(
    name="generate link conf",
    src="templates/pissnet-link.j2",
    dest="/tmp/link.conf",
    server_name=server_name,
    server_ip=v4_addr,
    autoconnect=False,
)

files.get(
    name="get link conf",
    src="/tmp/link.conf",
    dest=f"links/ec2-{v4_addr}.conf"
)

r.hset("pissnet:servers", server_name, v4_addr)
