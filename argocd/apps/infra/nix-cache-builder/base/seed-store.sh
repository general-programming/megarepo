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

# Root every image path. The store is otherwise entirely unrooted -- build
# outputs go to attic, not a profile -- so nix.conf's min-free GC is free to
# trim it, and without these roots it would happily delete the bash/git/curl
# this pod is about to run.
install -d /mnt/nix/var/nix/gcroots/image
for p in /nix/store/*; do
  ln -sfn "$p" "/mnt/nix/var/nix/gcroots/image/${p##*/}"
done

echo "==> seed complete"
