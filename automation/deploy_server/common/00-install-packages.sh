#!/usr/bin/env bash
set -xe

# Shared
fail () {
  echo $1 >&2
  exit 1
}

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

export DEBIAN_FRONTEND=noninteractive

# Get the Debian arch from uname
case $(uname -m) in
    x86_64)
        DEBIAN_ARCH=amd64
        ;;
    i386|i686)
        DEBIAN_ARCH=i386
        ;;
    arm64|aarch64)
        DEBIAN_ARCH=arm64
        ;;
    *)
        echo "Unsupported architecture: $(uname -m)"
        exit 1
        ;;
esac

# Get apt sources.list pre-install
cat /etc/apt/sources.list

# Update apt sources and install latest packages.
dpkg_waitlock apt-get -o DPkg::Lock::Timeout=-1 update
dpkg_waitlock apt-get -o DPkg::Lock::Timeout=-1 -y dist-upgrade

# Install software-properties-common and other apt goodies.
dpkg_waitlock apt-get -o DPkg::Lock::Timeout=-1 -y install apt-transport-https gnupg-agent software-properties-common ca-certificates curl git

# Install Ansible repo
dpkg_waitlock add-apt-repository --yes ppa:ansible/ansible

# Install HashiCorp repo
curl -fsSL https://apt.releases.hashicorp.com/gpg | apt-key add -
# TODO(erin) add arm64 support since it is supported https://github.com/hashicorp/consul/issues/9542
echo "deb [arch=amd64] https://apt.releases.hashicorp.com $(lsb_release -cs) main" > /etc/apt/sources.list.d/hashicorp.list

# Install Netmaker repo
curl -sL 'https://apt.netmaker.org/gpg.key' | sudo tee /etc/apt/trusted.gpg.d/netclient.asc
curl -sL 'https://apt.netmaker.org/debian.deb.txt' | sudo tee /etc/apt/sources.list.d/netclient.list

# Install our packages
dpkg_waitlock apt-get -o DPkg::Lock::Timeout=-1 update
dpkg_waitlock apt-get -o DPkg::Lock::Timeout=-1 -y install netclient nomad vault consul fail2ban docker.io mosh \
  ndppd git mosh traceroute htop cloud-init byobu ansible wireguard dnsutils python3-netaddr

# Setup consul folders
mkdir /var/lib/consul
chown consul:consul /var/lib/consul

# Systemd enable services now
if [ -x "$(command -v systemctl)" ]; then
    # Enable docker for plugin install
    systemctl enable --now docker
fi

# Install Docker plugins
docker plugin install grafana/loki-docker-driver:latest --alias loki --grant-all-permissions
