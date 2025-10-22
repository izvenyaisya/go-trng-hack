#!/usr/bin/env bash
set -euo pipefail

REPO="third_party/NIST-Statistical-Test-Suite"
LIB="$REPO/libnist.a"
OBJDIR="build/nist-objs"

if [ ! -d "$REPO" ]; then
  echo "Error: NIST repo not found at $REPO" >&2
  exit 2
fi

SRCS=$(find "$REPO" -type f -name '*.c')
if [ -z "$SRCS" ]; then
  echo "No .c sources found under $REPO" >&2
  exit 3
fi

mkdir -p "$OBJDIR"
rm -f "$OBJDIR"/*.o || true
rm -f "$LIB" || true

echo "Compiling ${REPO} C sources into static library ${LIB}..."
for src in $SRCS; do
  base=$(basename "$src")
  obj="$OBJDIR/${base%.c}.o"
  echo "  gcc -c -O2 -std=c11 -I\"$REPO\" -o \"$obj\" \"$src\""
  gcc -c -O2 -std=c11 -I"$REPO" -o "$obj" "$src"
done

echo "Archiving objects into ${LIB}..."
ar rcs "$LIB" $OBJDIR/*.o

echo "Built $LIB"
