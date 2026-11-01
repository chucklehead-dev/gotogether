#!/usr/bin/env python3
"""Plant one expiry-boundary defect in a disposable pinned dependency copy."""
import hashlib
import json
from pathlib import Path
import shutil
import sys

source, target = map(Path, sys.argv[1:])
file = Path('exploration/tail_membership.go')
original = (source / file).read_bytes()
digest = hashlib.sha256(original).hexdigest()
expected = 'd9c42d8737b53f265eac71b2284da52dc198db1c297453232caefe0e98974c65'
if digest != expected:
    raise SystemExit('pinned Goo source does not match the frozen contrast prerequisite')
before = b'if !now.Before(e.capability.ExpiresAt) {'
after = b'if now.After(e.capability.ExpiresAt) {'
if original.count(before) != 1:
    raise SystemExit('require exactly one expiry-pruning predicate')
shutil.copytree(source, target)
# Module-cache directories are read-only. Only the disposable copy is made
# writable so its owner can remove it after the contrast.
target.chmod(0o755)
for directory in target.rglob('*'):
    if directory.is_dir():
        directory.chmod(0o755)
changed = original.replace(before, after, 1)
(target / file).chmod(0o644)
(target / file).write_bytes(changed)
provenance = {
    'repository': 'github.com/casselc/goo',
    'revision': '0659a4b20fb7fe83bbfd32a194ceb9a7d0c339a9',
    'freezeCommit': 'ec382346',
    'file': str(file),
    'originalSHA256': digest,
    'contrastSHA256': hashlib.sha256(changed).hexdigest(),
    'original': before.decode(), 'contrast': after.decode(),
    'claim': 'parent-planted defect: exact-expiry proof survives pruning; not a production bug finding',
}
(target.parent / 'PROVENANCE.json').write_text(json.dumps(provenance, indent=2) + '\n')
