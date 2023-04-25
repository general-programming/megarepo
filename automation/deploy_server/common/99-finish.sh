#!/usr/bin/env bash
set -x
set -e

# Shared
retry () {
  local n=1
  local max=5
  local delay=15
  while true; do
    "$@" && break || {
      if [[ $n -lt $max ]]; then
        ((n++))
        echo "Command failed. Attempt $n/$max:"
        sleep $delay;
      else
        fail "The command has failed after $n attempts."
      fi
    }
  done
}

dpkg_waitlock () {
    while fuser /var/{lib/{dpkg,apt/lists},cache/apt/archives}/lock >/dev/null 2>&1; do
        sleep 1
    done

    eval "retry $@"
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

    # Enable misc services
    systemctl enable --now fail2ban
fi

# Create folders for support services.
mkdir /var/lib/filebeat || true # Filebeat

# Cleanup netclient stuff
systemctl stop netclient
rm -rf /etc/netclient

# Cleanup cloud-init
cloud-init clean
