# Container lx config flags
```
lxc.cgroup.devices.allow = c 10:200 rwm
lxc.mount.entry = /dev/net/tun dev/net/tun none bind,create=file
lxc.apparmor.profile: lxc-container-default-with-nfs
mp0:/mnt/nfs,mp=/mnt/nfs
```
