#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0




























set -euo pipefail

SRC="${1:?usage: pack.sh <plugin-dir> [out-dir]}"
OUT="${2:-dist}"

[ -d "$SRC" ] || { echo "pack.sh: $SRC is not a directory" >&2; exit 2; }
[ -f "$SRC/manifest.yaml" ] || {
  echo "pack.sh: $SRC has no manifest.yaml — that file is what makes a directory a facility" >&2
  exit 2
}

NAME="$(basename "$SRC")"
mkdir -p "$OUT"
ZIP="$OUT/$NAME.zip"

python3 - "$SRC" "$ZIP" <<'PY'
import os, sys, zipfile
src, out = sys.argv[1], sys.argv[2]
names = []
for root, dirs, files in os.walk(src):
    dirs.sort()
    for f in sorted(files):
        p = os.path.join(root, f)
        if os.path.islink(p):
            # A link names bytes that are not in the facility, and the node
            # refuses one in the ledger walk. Refusing here means the author
            # learns it on their own machine instead of on a node.
            sys.exit(f"pack.sh: {p} is a symbolic link")
        names.append((os.path.relpath(p, src).replace(os.sep, "/"), p))
names.sort()
with zipfile.ZipFile(out, "w", zipfile.ZIP_DEFLATED) as z:
    for arc, p in names:
        # A fixed timestamp: the digest must depend on the content and nothing
        # else. 1980-01-01 is zip's own epoch, so it is the one date that
        # cannot be mistaken for a real one.
        zi = zipfile.ZipInfo(arc, date_time=(1980, 1, 1, 0, 0, 0))
        zi.external_attr = 0o644 << 16
        zi.compress_type = zipfile.ZIP_DEFLATED
        with open(p, "rb") as fh:
            z.writestr(zi, fh.read())
PY

DIGEST="$(python3 -c '
import hashlib,sys
print(hashlib.sha256(open(sys.argv[1],"rb").read()).hexdigest())' "$ZIP")"

echo "$ZIP"
echo "sha256 $DIGEST"
echo "baton resource publish $ZIP --type plugin --name $NAME"
