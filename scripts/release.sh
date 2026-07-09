#!/usr/bin/env bash
set -euo pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$ROOT"

version=$(node -p "require('./npm/package.json').version")
tag="v$version"
repo="YingSuiAI/dirextalk-connect"
make_version=$(awk '/^VERSION[[:space:]]*:=/{print $3}' Makefile)

if [ "$make_version" != "$tag" ]; then
  echo "VERSION mismatch: Makefile has $make_version, npm/package.json has $version" >&2
  exit 1
fi

if ! git diff --ignore-cr-at-eol --quiet || ! git diff --cached --quiet; then
  echo "working tree is dirty; commit or stash changes before release" >&2
  exit 1
fi

command -v gh >/dev/null 2>&1 || { echo "gh is required" >&2; exit 1; }
command -v npm >/dev/null 2>&1 || { echo "npm is required" >&2; exit 1; }
command -v node >/dev/null 2>&1 || { echo "node is required" >&2; exit 1; }
command -v make >/dev/null 2>&1 || { echo "make is required" >&2; exit 1; }

echo "Running release checks for $tag..."
go test ./tests/release_local/... -count=1
node --check npm/install.js
node --check npm/check-release.js
npm pack --dry-run --prefix npm >/dev/null 2>&1 || (cd npm && npm pack --dry-run >/dev/null)
if ! git diff --ignore-cr-at-eol --quiet; then
  git diff --check
fi

echo "Building release assets..."
if ! make release-all; then
  if command -v powershell.exe >/dev/null 2>&1 || command -v powershell >/dev/null 2>&1; then
    echo "make release-all failed; attempting Windows zip fallback..."
  else
    exit 1
  fi
fi

powershell_cmd=""
if command -v powershell.exe >/dev/null 2>&1; then
  powershell_cmd=powershell.exe
elif command -v powershell >/dev/null 2>&1; then
  powershell_cmd=powershell
elif [ -x /mnt/c/Windows/System32/WindowsPowerShell/v1.0/powershell.exe ]; then
  powershell_cmd=/mnt/c/Windows/System32/WindowsPowerShell/v1.0/powershell.exe
fi

for arch in amd64 arm64; do
  exe="dist/dirextalk-connect-$tag-windows-$arch.exe"
  zip="dist/dirextalk-connect-$tag-windows-$arch.zip"
  if [ -f "$exe" ] && [ ! -f "$zip" ]; then
    if [ -n "$powershell_cmd" ]; then
      "$powershell_cmd" -NoProfile -Command "Compress-Archive -LiteralPath '$exe' -DestinationPath '$zip' -Force"
    elif command -v python3 >/dev/null 2>&1; then
      python3 - "$exe" "$zip" <<'PY'
import os
import sys
import zipfile

source, target = sys.argv[1], sys.argv[2]
with zipfile.ZipFile(target, "w", compression=zipfile.ZIP_DEFLATED) as archive:
    archive.write(source, arcname=os.path.basename(source))
PY
    else
      echo "missing zip asset and no PowerShell or python3 fallback: $zip" >&2
      exit 1
    fi
  fi
done

(cd dist && sha256sum * > checksums.txt)

expected=(
  checksums.txt
  "dirextalk-connect-$tag-darwin-amd64"
  "dirextalk-connect-$tag-darwin-amd64.tar.gz"
  "dirextalk-connect-$tag-darwin-arm64"
  "dirextalk-connect-$tag-darwin-arm64.tar.gz"
  "dirextalk-connect-$tag-linux-amd64"
  "dirextalk-connect-$tag-linux-amd64.tar.gz"
  "dirextalk-connect-$tag-linux-arm64"
  "dirextalk-connect-$tag-linux-arm64.tar.gz"
  "dirextalk-connect-$tag-windows-amd64.exe"
  "dirextalk-connect-$tag-windows-amd64.zip"
  "dirextalk-connect-$tag-windows-arm64.exe"
  "dirextalk-connect-$tag-windows-arm64.zip"
)

for asset in "${expected[@]}"; do
  [ -f "dist/$asset" ] || { echo "missing release asset: dist/$asset" >&2; exit 1; }
done

git fetch --tags origin
if git rev-parse "$tag" >/dev/null 2>&1 || git ls-remote --exit-code --tags origin "refs/tags/$tag" >/dev/null 2>&1; then
  echo "tag already exists: $tag"
else
  git tag -a "$tag" -m "dirextalk-connect $tag"
  git push origin "$tag"
fi

if gh release view "$tag" --repo "$repo" >/dev/null 2>&1; then
  echo "GitHub release already exists: $tag"
else
  gh release create "$tag" dist/* --repo "$repo" --title "dirextalk-connect $tag" --notes "Release assets for npm package dirextalk-connect@$version." --latest
fi

(cd npm && node check-release.js)
(cd npm && npm publish --access public)

tmp=$(mktemp -d)
cleanup() { rm -rf "$tmp"; }
trap cleanup EXIT
npm install --prefix "$tmp" "dirextalk-connect@$version"
"$tmp/node_modules/.bin/dirextalk-connect" --version

echo "Released dirextalk-connect@$version with GitHub $tag and npm latest."
