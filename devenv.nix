{ pkgs, lib, config, inputs, ... }:

let
  # nixpkgs' pulumi-bin lags upstream releases; track the latest official
  # release directly instead of waiting on nixpkgs to catch up. Bump
  # pulumiVersion + re-prefetch pulumiHashes (`nix store prefetch-file
  # --json <url>`) to update.
  pulumiVersion = "3.251.0";
  pulumiHashes = {
    x86_64-linux = "sha256-0agIs+S0vS+58O2/K1Je4VosIRTOT/iUtKXUmYZ/xAQ=";
    aarch64-linux = "sha256-O/tEGrwUJuaD3NTDS8tuXPAefS2eZ6WOC4/tvrt2yzA=";
    x86_64-darwin = "sha256-qNACo0F9c+yOCUsiv+ZwLVzcz+Rj00jJyIR2De+kdHE=";
    aarch64-darwin = "sha256-twOGn3E8oH+FC/FQ0zh5EmRPia25KD5ZXP7jj6fvhC4=";
  };
  pulumiPlatform = {
    x86_64-linux = "linux-x64";
    aarch64-linux = "linux-arm64";
    x86_64-darwin = "darwin-x64";
    aarch64-darwin = "darwin-arm64";
  }.${pkgs.stdenv.hostPlatform.system};

  pulumi-latest = pkgs.stdenv.mkDerivation {
    pname = "pulumi";
    version = pulumiVersion;

    src = pkgs.fetchurl {
      url = "https://github.com/pulumi/pulumi/releases/download/v${pulumiVersion}/pulumi-v${pulumiVersion}-${pulumiPlatform}.tar.gz";
      hash = pulumiHashes.${pkgs.stdenv.hostPlatform.system};
    };

    nativeBuildInputs = [ pkgs.makeWrapper ]
      ++ lib.optionals pkgs.stdenv.hostPlatform.isLinux [ pkgs.autoPatchelfHook ];
    buildInputs = lib.optionals pkgs.stdenv.hostPlatform.isLinux [ pkgs.stdenv.cc.cc.lib ];

    installPhase = ''
      mkdir -p $out/bin
      install -m755 * $out/bin/
    '' + lib.optionalString pkgs.stdenv.hostPlatform.isLinux ''
      wrapProgram $out/bin/pulumi --set LD_LIBRARY_PATH "${lib.getLib pkgs.stdenv.cc.cc}/lib"
    '';

    meta = {
      description = "Pulumi CLI, tracked at the latest upstream release (not nixpkgs)";
      homepage = "https://www.pulumi.com/";
      license = lib.licenses.asl20;
      platforms = builtins.attrNames pulumiHashes;
    };
  };
in
{
  # https://devenv.sh/basics/
  env.GREET = "devenv";

  # https://devenv.sh/packages/
  packages = with pkgs; [
    coder
    git
    talosctl
    # machine provisioning (nix/justfile)
    just
    vault-bin
    pulumi-latest
  ];

  # https://devenv.sh/languages/python/
  languages.python = {
    enable = true;
    venv.enable = true;
    uv = {
      enable = true;
      sync.enable = true;
    };
  };
}
