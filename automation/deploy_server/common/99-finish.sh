#!/bin/sh
set -x
set -e

# Shared
dpkg_waitlock () {
    while fuser /var/{lib/{dpkg,apt/lists},cache/apt/archives}/lock >/dev/null 2>&1; do
        sleep 1
    done

    eval "$@"
}

# Dpkg based
if [ -x "$(command -v apt-get)" ]; then
    dpkg_waitlock apt-get -o DPkg::Lock::Timeout=-1 -y autoremove
    # Cleanup apt packages.
    dpkg_waitlock apt-get -o DPkg::Lock::Timeout=-1 clean
fi

# Systemd
if [ -x "$(command -v systemctl)" ]; then
    # Enable docker + nomad
    systemctl enable --now docker
    systemctl enable nomad
    systemctl enable consul
fi

# Create folders for support services.
mkdir /var/lib/filebeat || true # Filebeat

# Cleanup cloud-init
cloud-init clean
