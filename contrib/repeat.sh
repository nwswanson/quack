#!/usr/bin/env sh
# repeat.sh <CHAR> <COUNT>

char=$1
count=$2

case "$count" in
  ''|*[!0-9]*)
    echo "usage: $0 <CHAR> <COUNT>" >&2
    exit 1
    ;;
esac

awk -v c="$char" -v n="$count" 'BEGIN {
  for (i = 0; i < n; i++) printf "%s", c
}'
