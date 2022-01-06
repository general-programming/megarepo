import redis
from pyinfra import host

r = redis.Redis()

output = host.fact.command("ip -j -s -s link show dev ens5")
r.hset("ipstats", host.fact.hostname, output)
