#!/bin/bash

# Sync version from git tag to PyPI package metadata.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(dirname "$SCRIPT_DIR")"
PYPI_DIR="$PROJECT_ROOT/pypi"
PYPROJECT="$PYPI_DIR/pyproject.toml"
INIT_FILE="$PYPI_DIR/src/mothx_installer/__init__.py"
PYTHON="${PYTHON:-python3}"

if [ -n "${1:-}" ]; then
  VERSION="$1"
else
  VERSION=$(git describe --tags --always 2>/dev/null || true)
  if [ -z "$VERSION" ]; then
    echo "Error: Could not determine version"
    exit 1
  fi
fi

VERSION="${VERSION#v}"
VERSION="${VERSION%-dirty}"
VERSION="${VERSION%%-[0-9]*-g[0-9a-f]*}"

# npm pre-release tags use "-pre-N" (or bare "-pre"); PyPI requires PEP 440
# "rcN" (or an explicit rc number for a bare "-pre"). Keep the numeric
# pre-release ordinal instead of collapsing every pre-release into rc0.
case "$VERSION" in
  *-pre-[0-9]*)
    VERSION=$(printf '%s' "$VERSION" | sed -E 's/-pre-([0-9]+)$/rc\1/')
    ;;
  *-pre)
    VERSION="${VERSION%-pre}rc1"
    ;;
  *-alpha-[0-9]*)
    VERSION=$(printf '%s' "$VERSION" | sed -E 's/-alpha-([0-9]+)$/a\1/')
    ;;
  *-alpha)
    VERSION="${VERSION%-alpha}a1"
    ;;
  *-beta-[0-9]*)
    VERSION=$(printf '%s' "$VERSION" | sed -E 's/-beta-([0-9]+)$/b\1/')
    ;;
  *-beta)
    VERSION="${VERSION%-beta}b1"
    ;;
  *-dev)
    VERSION="${VERSION%-dev}.dev0"
    ;;
esac

if [ "$VERSION" = "dev" ]; then
  VERSION="0.0.0.dev0"
fi

echo "Syncing PyPI version to: $VERSION"

"$PYTHON" - "$PYPROJECT" "$INIT_FILE" "$VERSION" <<'PY'
from pathlib import Path
import re
import sys

pyproject = Path(sys.argv[1])
init_file = Path(sys.argv[2])
version = sys.argv[3]

text = pyproject.read_text()
updated, count = re.subn(r'(?m)^version = "[^"]+"', f'version = "{version}"', text, count=1)
if count == 0:
    raise SystemExit(f"version field not found: {pyproject}")
pyproject.write_text(updated)
print(f"Updated: {pyproject}")

text = init_file.read_text()
updated, count = re.subn(r'(?m)^__version__ = "[^"]+"', f'__version__ = "{version}"', text, count=1)
if count == 0:
    raise SystemExit(f"__version__ field not found: {init_file}")
init_file.write_text(updated)
print(f"Updated: {init_file}")
PY

echo "PyPI version sync complete: $VERSION"
