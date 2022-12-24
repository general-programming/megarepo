#!/usr/bin/env bash
fallocate -l 512M /swapfile
chmod 600 /swapfile
mkswap /swapfile
swapon /swapfile

grep -qxF '/swapfile swap swap defaults 0 0' /etc/fstab || echo '/swapfile swap swap defaults 0 0' >> /etc/fstab
