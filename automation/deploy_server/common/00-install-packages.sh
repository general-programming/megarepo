#!/bin/sh
set -xe

# Shared
dpkg_waitlock () {
    while fuser /var/{lib/{dpkg,apt/lists},cache/apt/archives}/lock >/dev/null 2>&1; do
        sleep 1
    done

    eval "$@"
}

export DEBIAN_FRONTEND=noninteractive

# Update apt sources and install latest packages.
dpkg_waitlock apt-get -o DPkg::Lock::Timeout=-1 update
dpkg_waitlock apt-get -o DPkg::Lock::Timeout=-1 -y dist-upgrade

# Install software-properties-common and other apt goodies.
dpkg_waitlock apt-get -o DPkg::Lock::Timeout=-1 -y install apt-transport-https gnupg-agent software-properties-common ca-certificates curl git

# Install HashiCorp repo
curl -fsSL https://apt.releases.hashicorp.com/gpg | apt-key add -
echo "deb [arch=amd64] https://apt.releases.hashicorp.com $(lsb_release -cs) main" > /etc/apt/sources.list.d/hashicorp.list
dpkg_waitlock apt-get -o DPkg::Lock::Timeout=-1 update
dpkg_waitlock apt-get -o DPkg::Lock::Timeout=-1 -y install nomad vault consul

# Install Ansible repo
dpkg_waitlock add-apt-repository --yes --update ppa:ansible/ansible

# Install our packages
dpkg_waitlock apt-get -o DPkg::Lock::Timeout=-1 -y install fail2ban docker.io mosh python3-virtualenv ndppd git mosh traceroute htop cloud-init byobu ansible

# Install pyinfra in /root
virtualenv -p python3 /root/pyenv
/root/pyenv/bin/pip install pyinfra hvac

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
