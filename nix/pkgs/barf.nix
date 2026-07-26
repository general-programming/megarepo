# barf, the network config generator, as a package machines can depend on.
#
# `src` is deliberately narrow: with the whole repo as source, any commit —
# argocd, docs, terraform — would change barf's hash, rebuild it, and have
# comin redeploy dnsmasq and Kea on the core machines for a change that never
# touched them.
{
  lib,
  buildGoModule,
}:

buildGoModule {
  pname = "barf";
  version = "0.1.0";

  src = lib.fileset.toSource {
    root = ../..;
    fileset = lib.fileset.unions [
      ../../go
      ../../go.mod
      ../../go.sum
    ];
  };

  # Bump alongside go.mod/go.sum: `nix build ./nix#barf` prints the new hash
  # on mismatch.
  vendorHash = "sha256-hSGkTIPE4sjnlvNsvUgjgCCqKMUX5zeHMByh5EvZKdE=";

  subPackages = [ "go/cmd/barf" ];

  # The render tests read projects/barf/network.yml, which is outside `src`
  # on purpose. The go workflow runs `go test ./go/...` with the full tree.
  doCheck = false;

  ldflags = [
    "-s"
    "-w"
  ];

  meta = {
    description = "Renders network device, dnsmasq and Kea config from NetBox and network.yml";
    mainProgram = "barf";
  };
}
