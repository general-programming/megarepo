#!/bin/sh

echo 'net.ipv4.ip_forward=1' > /etc/sysctl.d/99-enable-ipv4-forwarding.conf
echo 'net.ipv4.conf.all.forwarding=1' >> /etc/sysctl.d/99-enable-ipv4-forwarding.conf
echo 'vm.swappiness=1' > /etc/sysctl.d/99-swappiness.conf

sysctl --system
