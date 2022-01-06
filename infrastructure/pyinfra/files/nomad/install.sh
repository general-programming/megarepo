#!/bin/sh

ARCH=$(uname -m)

if [ ! -f "/usr/local/bin/nomad" ]; then
    if [ "$ARCH" = "aarch64" ]; then
        NOMAD_ZIP="https://releases.hashicorp.com/nomad/1.1.1/nomad_1.1.1_linux_arm64.zip"
    else
        NOMAD_ZIP="https://releases.hashicorp.com/nomad/1.1.1/nomad_1.1.1_linux_amd64.zip"
    fi

    wget -O /tmp/nomad.zip "$NOMAD_ZIP"
    unzip /tmp/nomad.zip
    mv nomad /usr/local/bin/nomad
    chmod +x /usr/local/bin/nomad
fi
