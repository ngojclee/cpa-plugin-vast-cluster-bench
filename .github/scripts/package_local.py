#!/usr/bin/env python3
"""Package the CPA plugin .so into the store zip format + sha256 checksums.

Mirrors ref-go-pool/.github/scripts/package-release.go:
- zip contains one root-level entry named <plugin-id>.so (mode 0755)
- sha256 of the zip written to <archive>.sha256 and dist/checksums.txt
"""
import hashlib
import os
import sys
import zipfile

PLUGIN_ID = "vast-cluster-bench"
VERSION = "0.2.3"
_THIS = os.path.abspath(__file__)           # .../cpa-plugin-vast-cluster-bench/.github/scripts/package_local.py
HERE = os.path.dirname(os.path.dirname(os.path.dirname(_THIS)))  # repo root
DIST = os.path.join(HERE, "dist")

so_path = os.path.join(DIST, f"{PLUGIN_ID}-v{VERSION}.so")
zip_path = os.path.join(DIST, f"{PLUGIN_ID}_{VERSION}_linux_amd64.zip")
sha_path = zip_path + ".sha256"

if not os.path.exists(so_path):
    sys.exit(f"missing {so_path}")

# zip with one root-level entry: <plugin-id>.so (no version prefix)
with zipfile.ZipFile(zip_path, "w", zipfile.ZIP_DEFLATED) as zf:
    zi = zipfile.ZipInfo(f"{PLUGIN_ID}.so")
    zi.external_attr = 0o755 << 16
    with open(so_path, "rb") as f:
        zf.writestr(zi, f.read())

# checksum of the zip
data = open(zip_path, "rb").read()
digest = hashlib.sha256(data).hexdigest()
line = f"{digest}  {os.path.basename(zip_path)}\n"
open(sha_path, "w").write(line)
open(os.path.join(DIST, "checksums.txt"), "w").write(line)

print("zip:", zip_path, os.path.getsize(zip_path), "bytes")
print("sha:", digest)
print("checksums.txt written")
