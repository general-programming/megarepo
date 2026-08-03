#!/bin/bash
# Install the SN200 Xtensa FLIX length fix into a Ghidra installation and
# recompile the SLEIGH spec.  See docs/xtensa-flix-decoding.md.
#
#   ./install.sh [/path/to/ghidra]       install the fix
#   ./install.sh [/path/to/ghidra] undo  restore stock Ghidra behaviour
set -euo pipefail

GH=${1:-/Users/nep/Downloads/ghidra_12.1.2_PUBLIC}
MODE=${2:-install}
HERE=$(cd "$(dirname "$0")" && pwd)
LANGS=$GH/Ghidra/Processors/Xtensa/data/languages

[ -d "$LANGS" ] || { echo "no Xtensa processor module at $LANGS" >&2; exit 1; }

# keep a pristine copy the first time we touch this install
if [ ! -f "$LANGS/flix.sinc.stock" ]; then
	cp "$LANGS/flix.sinc" "$LANGS/flix.sinc.stock"
	echo "saved stock spec -> $LANGS/flix.sinc.stock"
fi

case "$MODE" in
	install) cp "$HERE/languages/flix.sinc" "$LANGS/flix.sinc" ;;
	undo)    cp "$LANGS/flix.sinc.stock"    "$LANGS/flix.sinc" ;;
	*) echo "usage: $0 [ghidra_dir] [install|undo]" >&2; exit 1 ;;
esac

echo "recompiling SLEIGH..."
( cd "$LANGS" && "$GH/support/sleigh" -a . ) | tail -3

cat <<'EOF'

Done. Two things are required for this to take effect:

  1. Restart Ghidra. A running instance caches the compiled .sla in memory
     and will NOT pick up the new one.
  2. Existing programs keep their old disassembly in the project database.
     For each affected program:  Select All -> Clear Code Bytes (C),
     then re-run Auto Analysis.  Re-importing the binary also works.
EOF
