#!/usr/bin/env bash
# Build every machine's system closure and push it to Attic, so comin-managed
# hosts substitute instead of building. This is `just build_cache` (nix/justfile)
# run in-cluster; the build logic lives there, not here.
set -euo pipefail

REPO="${REPO:-https://github.com/general-programming/megarepo.git}"
BRANCH="${BRANCH:-main}"
STATE_DIR=/nix/var/nix-cache-builder
SRC="$STATE_DIR/megarepo"
STATE="$STATE_DIR/last-built-ref"

export HOME="$STATE_DIR/home"
export TMPDIR="$STATE_DIR/tmp"
install -d "$STATE_DIR" "$HOME" "$TMPDIR"

# The repo is public, so the checkout needs no credentials. Keep the clone on
# the PVC: fetching a delta beats re-cloning every five minutes.
if [ -d "$SRC/.git" ]; then
  git -C "$SRC" remote set-url origin "$REPO"
  git -C "$SRC" fetch --prune --quiet origin "$BRANCH"
else
  rm -rf "$SRC"
  git clone --quiet --branch "$BRANCH" "$REPO" "$SRC"
fi
git -C "$SRC" checkout --quiet --detach "origin/$BRANCH"
git -C "$SRC" clean -qfdx
head=$(git -C "$SRC" rev-parse HEAD)

last=""
[ -f "$STATE" ] && last=$(cat "$STATE")

if [ "$head" = "$last" ]; then
  echo "==> $head already built, nothing to do"
  exit 0
fi

# Only a change that can move a system closure is worth a build; anything else
# just advances the marker. Same path filter as .github/workflows/nix.yaml.
if [ -n "$last" ] && git -C "$SRC" cat-file -e "${last}^{commit}" 2>/dev/null; then
  if git -C "$SRC" diff --quiet "$last" "$head" -- nix go go.mod go.sum vendor; then
    echo "==> ${last:0:12}..${head:0:12} touches no nix/ or go/ path, skipping"
    echo "$head" >"$STATE"
    exit 0
  fi
fi

# Resolve `nixpkgs#...` (used by build_cache for attic-client and
# nix-output-monitor) to the flake's own locked nixpkgs instead of whatever
# nixpkgs-unstable happens to be today -- otherwise every run drags in a second
# nixpkgs tree.
nixpkgs=$(nix eval --raw --impure --expr "
  let
    lock = builtins.fromJSON (builtins.readFile $SRC/nix/flake.lock);
    node = lock.nodes.\${lock.nodes.\${lock.root}.inputs.nixpkgs};
    l = node.locked;
  in \"github:\${l.owner}/\${l.repo}/\${l.rev}\"
")
echo "==> pinning nixpkgs registry to $nixpkgs"
nix registry add nixpkgs "$nixpkgs"

echo "==> logging in to attic"
nix run "${nixpkgs}#attic-client" -- login general-programming \
  https://attic.owo.me/ "$ATTIC_TOKEN"

echo "==> building $head"
cd "$SRC/nix"
nix run "${nixpkgs}#just" -- build_cache

echo "$head" >"$STATE"
echo "==> done, cache is current as of $head"
