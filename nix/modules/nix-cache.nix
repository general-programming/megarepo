# Client-side configuration for our self-hosted Attic binary cache
# (argocd/apps/infra/attic, running in fmt2).
#
# Disabled until the cache is bootstrapped and its public key is known;
# see argocd/apps/infra/attic/README.md for the bootstrap runbook.

{
  lib,
  config,
  ...
}:

let
  cfg = config.gpNixCache;
in
{
  options.gpNixCache = {
    enable = lib.mkEnableOption "the general-programming Attic binary cache";

    url = lib.mkOption {
      type = lib.types.str;
      default = "https://attic.owo.me/general-programming";
      description = "Substituter URL (Attic endpoint + cache name).";
    };

    publicKey = lib.mkOption {
      type = lib.types.nullOr lib.types.str;
      default = null;
      description = "Cache signing public key, from `attic cache info general-programming`.";
    };
  };

  config = lib.mkMerge [
    {
      # Keep an unreachable substituter cheap: give up connecting after 5s
      # and fall back to building locally instead of failing.
      nix.settings = {
        connect-timeout = 5;
        fallback = true;
      };
    }

    (lib.mkIf cfg.enable {
      assertions = [
        {
          assertion = cfg.publicKey != null;
          message = "gpNixCache.publicKey must be set when gpNixCache.enable is true.";
        }
      ];

      nix.settings.substituters = [ cfg.url ];
      nix.settings.trusted-public-keys = [ cfg.publicKey ];
    })
  ];
}
