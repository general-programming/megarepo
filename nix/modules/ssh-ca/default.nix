# Trust the OpenBao SSH client CA (`ssh-client-signer`) for certificate logins,
# mirroring what salt/state/sshd_config does on the Debian fleet.
#
# The CA issues 30-minute certificates carrying the `admin` principal (role
# `ssh-client-signer/roles/administrator-role`). NixOS hosts have no `admin`
# user — root is the only account — so root's principals file lists `admin`
# and certificate holders land directly on root.
#
# SECURITY: this makes "can obtain an admin certificate from the CA" equivalent
# to "is root on every NixOS host". That is the same bargain the salt fleet
# already makes via /etc/sshd/auth_principals/admin + NOPASSWD sudo; it is
# written down here because on these boxes it skips the sudo step. Narrow it
# per-host with `sshCa.rootPrincipals = [ ]` to keep the CA trusted for other
# users while refusing it for root.
#
# The CA public key is checked in (it is public by definition). If the CA is
# ever rotated, re-export it:
#   bao read -field=public_key ssh-client-signer/config/ca > client-ca.pub

{
  lib,
  config,
  ...
}:

let
  cfg = config.sshCa;
in
{
  options.sshCa = {
    enable = lib.mkEnableOption "trusting the OpenBao SSH client CA" // {
      default = true;
    };

    publicKeyFile = lib.mkOption {
      type = lib.types.path;
      default = ./client-ca.pub;
      description = "Public key of the SSH client CA to trust.";
    };

    rootPrincipals = lib.mkOption {
      type = lib.types.listOf lib.types.str;
      default = [ "admin" ];
      description = ''
        Certificate principals allowed to authenticate as root. Empty disables
        certificate logins for root without untrusting the CA.
      '';
    };
  };

  config = lib.mkIf cfg.enable {
    environment.etc = {
      "ssh/vault_ca.pub".source = cfg.publicKeyFile;

      # sshd matches the cert's principals against this file rather than
      # requiring principal == username, which is what lets an `admin` cert
      # open a root session. Other users get no file, so their certificates
      # are refused; ordinary key auth is unaffected.
      "ssh/auth_principals/root" = lib.mkIf (cfg.rootPrincipals != [ ]) {
        text = lib.concatMapStrings (p: "${p}\n") cfg.rootPrincipals;
        mode = "0444";
      };
    };

    services.openssh.extraConfig = ''
      TrustedUserCAKeys /etc/ssh/vault_ca.pub
      AuthorizedPrincipalsFile /etc/ssh/auth_principals/%u
    '';
  };
}
