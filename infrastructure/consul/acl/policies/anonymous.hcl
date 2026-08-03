# Attached to the built-in "anonymous" token (AccessorID
# 00000000-0000-0000-0000-000000000002).
#
# READ THIS BEFORE TIGHTENING IT. Consul DNS on :8600 answers unauthenticated
# queries as the anonymous token. Every NixOS dnsmasq in the fleet forwards
#   /consul/                      -> 127.0.0.1#8600
#   /consul.generalprogramming.org/ -> 127.0.0.1#8600
# (nix/modules/dns/default.nix), so if these two reads go away the moment
# default_policy flips to "deny", .consul resolution stops fleet-wide -- not
# just in sea1. Keep them.
#
# Deliberately NOT granted here, all of which are wide open today:
#   - key_prefix  (KV is currently world-readable AND world-writable)
#   - node/service *write* (anyone can register or deregister catalog entries)
#   - agent_prefix write (anyone can force leave / reload a remote agent)
#   - operator (raft membership)
# Locking those down is most of the value of turning ACLs on at all.

node_prefix "" {
  policy = "read"
}

service_prefix "" {
  policy = "read"
}
