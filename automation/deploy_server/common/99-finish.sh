#!/bin/sh
set -x
set -e

# Dpkg based
if [ -x "$(command -v apt-get)" ]; then
    apt-get -y autoremove
    # Cleanup apt packages.
    apt-get clean
fi

# Systemd
if [ -x "$(command -v systemctl)" ]; then
    # Enable docker + nomad
    systemctl enable docker
    systemctl enable nomad
    systemctl enable consul
fi

# Create folders for support services.
mkdir /var/lib/filebeat || true # Filebeat

# Cleanup cloud-init
cloud-init clean
