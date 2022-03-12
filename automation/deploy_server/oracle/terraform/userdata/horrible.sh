#!/bin/sh

apt-get update
apt-get -y install cloud-init
wget -O /etc/cloud/cloud.cfg.d/99.cfg [REDACTED]

cloud-init clean
cloud-init -d init
cloud-init -d modules --mode config
cloud-init -d modules --mode final
