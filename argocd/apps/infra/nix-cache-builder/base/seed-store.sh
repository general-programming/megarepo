#!/usr/bin/env bash
# The PVC gets mounted over /nix, which would hide the image's own Nix. Copy the
# image store onto the PVC first (idempotent), and leave a DB dump behind so the
# build container can register anything an image bump added.
set -euo pipefail

if [ ! -d /mnt/nix/store ]; then
  echo "==> seeding empty /nix from the image"
  cp -a /nix/. /mnt/nix/
else
  echo "==> merging image store paths into the warm store"
  cp -a -n /nix/store/. /mnt/nix/store/
fi

nix-store --dump-db >/mnt/nix/.image-db.dump
echo "==> seed complete"
