FROM ubuntu:26.04

RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        ca-certificates \
        curl \
        git \
        sudo \
        xz-utils \
    && rm -rf /var/lib/apt/lists/*

# Ubuntu 24.04+ base images ship a default "ubuntu" user on uid/gid 1000,
# which must go before coder can claim those ids.
RUN userdel --remove ubuntu \
    && groupadd --gid 1000 coder \
    && useradd --uid 1000 --gid coder --shell /bin/bash --create-home coder \
    && echo "coder ALL=(ALL) NOPASSWD:ALL" > /etc/sudoers.d/coder

# /nix is pre-created and owned by uid 1000 so the single-user install below
# never needs to shell out to sudo/root.
RUN mkdir -m 0755 /nix && chown coder:coder /nix

USER coder
WORKDIR /home/coder

# Single-user, no-daemon install. The Determinate Systems installer has no
# equivalent mode (its --init none still yields a root-owned multi-user
# store), so this uses the traditional nixos.org install script instead.
RUN curl -L https://nixos.org/nix/install | sh -s -- --no-daemon

USER root

# Nix's default sandbox needs unprivileged user namespaces, which Talos
# blocks at the kernel level cluster-wide (user.max_user_namespaces = 0).
# Disabling the sandbox here is the workspace-local alternative to a
# cluster-wide sysctl change.
RUN mkdir -p /etc/nix \
    && printf '%s\n' \
        'sandbox = false' \
        'experimental-features = nix-command flakes' \
        > /etc/nix/nix.conf

# /home/coder is replaced by an empty persistent volume at runtime, so the
# ~/.nix-profile shell integration the installer just wrote there won't
# survive a real workspace boot — and modern Nix keeps single-user profiles
# under ~/.local/state/nix, not /nix, so there is no stable profile path in
# the persisted store to source from. Instead, resolve the nix package's
# /nix/store path right now at build time and bake it into a system-wide
# profile.d script; user profiles (once home-manager or nix profile create
# them in the persisted home) are prepended so they take precedence.
RUN mkdir -p /etc/profile.d \
    && NIX_BIN_DIR="$(dirname "$(readlink -f /home/coder/.nix-profile/bin/nix)")" \
    && printf '%s\n' \
        '#!/bin/sh' \
        "export PATH=\"$NIX_BIN_DIR:\$PATH\"" \
        'export NIX_SSL_CERT_FILE=/etc/ssl/certs/ca-certificates.crt' \
        'if [ -d "$HOME/.local/state/nix/profiles/profile/bin" ]; then PATH="$HOME/.local/state/nix/profiles/profile/bin:$PATH"; fi' \
        'if [ -d "$HOME/.nix-profile/bin" ]; then PATH="$HOME/.nix-profile/bin:$PATH"; fi' \
        > /etc/profile.d/nix.sh \
    && chmod 0644 /etc/profile.d/nix.sh \
    && head -2 /etc/profile.d/nix.sh | grep -q '/nix/store/'

USER coder
