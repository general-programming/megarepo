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
      ../../vendor
    ];
  };

  # null, not a hash: deps come from the checked-in vendor/, so there is no
  # fixed-output derivation to re-hash on every bump — and no network fetch at
  # build time, which matters because comin builds on the machines and Lix FOD
  # fetches need /dev/net/tun (absent in the LXC builder).
  # Renovate re-vendors via postUpdateOptions; if a bump ever lands without it,
  # the build fails with "inconsistent vendoring" and `go mod vendor` fixes it.
  vendorHash = null;

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
