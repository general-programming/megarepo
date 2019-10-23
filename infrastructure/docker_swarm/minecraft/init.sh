#!/bin/sh

if [ ! -f /etc/ssh/sshd_config ]; then
    cp /usr/share/openssh/sshd_config /etc/ssh/
    dpkg-reconfigure openssh-server
fi

service ssh start

byobu new-session -d -s "server" "/data/start.sh"
tail -f /var/log/*
