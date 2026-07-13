# saltext.vault is not packaged in nixpkgs; it is injected into salt's
# Python environment via the salt `extraInputs` override (see default.nix
# in this directory), matching the legacy master's `salt-pip install
# saltext-vault`.
{ python3Packages, fetchPypi }:

python3Packages.buildPythonPackage rec {
  pname = "saltext-vault";
  version = "1.7.0";
  pyproject = true;

  src = fetchPypi {
    pname = "saltext_vault";
    inherit version;
    hash = "sha256-KSuisTRaJy8tyO8HO+oU8ioR7aueitHQOmasOqphA0k=";
  };

  build-system = with python3Packages; [
    setuptools
    setuptools-scm
  ];

  # `salt` is the host application this extension is loaded into;
  # depending on it here would be circular.
  pythonRemoveDeps = [ "salt" ];

  dependencies = with python3Packages; [
    cryptography
  ];

  # Tests need a live salt master/minion factory, and importing the
  # package pulls in salt itself.
  doCheck = false;
}
