import redis
from pyinfra import host
from pyinfra.facts.server import Command, Hostname

r = redis.Redis()

output = host.get_fact(Command, "ip -j -s -s link show dev ens5")
r.hset("ipstats", host.get_fact(Hostname), output)
