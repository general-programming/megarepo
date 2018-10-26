#!/bin/sh
set -e

echo "Path: $PATH"

if [ $(echo "$1" | cut -c1) = "-" ]; then
  echo "$0: assuming arguments for coind"

  set -- coind "$@"
fi

if [ $(echo "$1" | cut -c1) = "-" ] || [ "$1" = "coind" ]; then
  mkdir -p "$COIN_DATA"
  chmod 700 "$COIN_DATA"
  chown -R coin "$COIN_DATA"

  echo "$0: setting data directory to $COIN_DATA"

  set -- "$@" -datadir="$COIN_DATA"
fi

if [ "$1" = "coind" ] || [ "$1" = "coin-cli" ] || [ "$1" = "coin-tx" ]; then
  echo
  set -- gosu coin "$@"
fi

echo
exec "$@"
