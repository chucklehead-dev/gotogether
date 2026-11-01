#!/usr/bin/env python3
"""Plant an arithmetic contrast in a disposable copy of the pinned library."""
import hashlib
import json
import os
from pathlib import Path
import shutil
import sys

source, target = map(Path, sys.argv[1:])
relative = Path('exploration/counted_values.go')
original = (source / relative).read_bytes()
assert hashlib.sha256(original).hexdigest() == '3571519c230e307e941f67ef67d421f57d7bedae0247ea70f1ff2df9fc046dbb'
changed = original
for before, after in (
    (b'\t"math/big"\n', b''),
    (b'sum := new(big.Int)', b'var sum uint64'),
    (b'sum.Add(sum, new(big.Int).SetUint64(n))', b'sum += n'),
    (b'sum.Cmp(new(big.Int).SetUint64(count)) != 0', b'sum != count'),
):
    assert changed.count(before) == 1, before
    changed = changed.replace(before, after, 1)
shutil.copytree(source, target)
# Go module-cache directories are read-only; only this disposable copy changes.
for directory, _, _ in os.walk(target):
    Path(directory).chmod(0o755)
(target / relative).chmod(0o644)
(target / relative).write_bytes(changed)
(target.parent / 'PROVENANCE.json').write_text(json.dumps({
    'module': 'github.com/casselc/goo',
    'revision': '0659a4b20fb7fe83bbfd32a194ceb9a7d0c339a9',
    'file': str(relative),
    'originalSHA256': hashlib.sha256(original).hexdigest(),
    'contrastSHA256': hashlib.sha256(changed).hexdigest(),
    'change': 'replace mathematical group sum with wrapping uint64 addition',
}, indent=2) + '\n')
