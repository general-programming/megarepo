#!/bin/sh

if [ ! -f /etc/ssh/sshd_config ]; then
    cp /etc/system_sshd_config /etc/ssh/
    ssh-keygen -A
fi

service ssh start || /usr/sbin/sshd &

byobu new-session -d -s "server" "/data/start.sh"
tail -f /var/log/*

while true; do sleep 10000; done
