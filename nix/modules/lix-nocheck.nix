# Lix is pinned to git main, so no substituter has it and every host compiles
# it. Its functional2 suite then deadlocks its pytest workers, which on the
# sea1 cache builder means a wedged job every run. Lix dogfoods itself as the
# nix that builds the fleet, so a green test suite here buys us nothing that
# actually using it does not.
{ lib, ... }:

{
  # mkAfter, not a bare list: lix-module's own overlay assigns `lix` outright,
  # so an override merged ahead of it is silently thrown away.
  nixpkgs.overlays = lib.mkAfter [
    (_final: prev: {
      lix = prev.lix.overrideAttrs (_old: {
        doCheck = false;
        doInstallCheck = false;
      });
    })
  ];
}
