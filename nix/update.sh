#!/usr/bin/env bash

set -xe

cd "$(dirname "$0")"

nix flake update
nix run 'nixpkgs#nixos-rebuild' -- --flake .#fmt2-core --target-host root@79.110.170.3 switch
