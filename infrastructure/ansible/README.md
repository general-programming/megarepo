# Ansible
Bad stuff but it automates some tedious infra work.

## Setup
```sh
ansible-galaxy collection install netbox.netbox
```

## Netbox tags
* dnsserver - Deploys DNS entries from Netbox.
* dhcpserver - Deploys static DHCP leases to this server.

## Container lx config flags
```
lxc.cgroup.devices.allow = c 10:200 rwm
lxc.mount.entry = /dev/net/tun dev/net/tun none bind,create=file
lxc.apparmor.profile: lxc-container-default-with-nfs
mp0:/mnt/nfs,mp=/mnt/nfs
```
