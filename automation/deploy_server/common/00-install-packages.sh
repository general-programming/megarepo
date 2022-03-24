#!/bin/sh
set -xe

export DEBIAN_FRONTEND=noninteractive

# Update apt sources and install latest packages.
apt-get -o DPkg::Lock::Timeout=-1 update
apt-get -o DPkg::Lock::Timeout=-1 -y dist-upgrade

# Install software-properties-common and other apt goodies.
apt-get -o DPkg::Lock::Timeout=-1 -y install apt-transport-https gnupg-agent software-properties-common ca-certificates curl

# Install HashiCorp repo
curl -fsSL https://apt.releases.hashicorp.com/gpg | apt-key add -
echo "deb [arch=amd64] https://apt.releases.hashicorp.com $(lsb_release -cs) main" > /etc/apt/sources.list.d/hashicorp.list
apt-get -o DPkg::Lock::Timeout=-1 update
apt-get -o DPkg::Lock::Timeout=-1 -y install nomad vault consul

# Install Ansible repo
add-apt-repository --yes --update ppa:ansible/ansible

# Install our packages
apt-get -o DPkg::Lock::Timeout=-1 -y install fail2ban docker.io mosh python3-virtualenv ndppd git mosh traceroute htop cloud-init byobu ansible

# Install pyinfra in /root
virtualenv -p python3 /root/pyenv
/root/pyenv/bin/pip install pyinfra

# Setup consul folders
mkdir /var/lib/consul
chown consul:consul /var/lib/consul
