FROM ubuntu:26.04

RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        ca-certificates \
        curl \
        git \
        sudo \
        xz-utils \
    && rm -rf /var/lib/apt/lists/*

RUN groupadd --gid 1000 coder \
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
# survive a real workspace boot. A system-wide profile.d script sourced from
# a fixed path under /nix (which does persist) fixes this for every shell.
# The template can rename uid 1000 away from "coder" (home_user parameter),
# so try the current $USER's per-user profile first and fall back to the
# "coder" profile the seed image ships with.
RUN mkdir -p /etc/profile.d \
    && printf '%s\n' \
        '#!/bin/sh' \
        'for _nix_user in "$USER" coder; do' \
        '  _nix_sh="/nix/var/nix/profiles/per-user/$_nix_user/profile/etc/profile.d/nix.sh"' \
        '  if [ -e "$_nix_sh" ]; then . "$_nix_sh"; break; fi' \
        'done' \
        'unset _nix_user _nix_sh' \
        > /etc/profile.d/nix.sh \
    && chmod 0644 /etc/profile.d/nix.sh

USER coder
