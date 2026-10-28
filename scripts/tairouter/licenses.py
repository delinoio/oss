#!/usr/bin/env python3
"""Collect complete upstream license/notice texts from downloaded Go modules."""
import json
import os
from pathlib import Path
import subprocess


def bundle(root, destination):
    # Include all resolved modules, including platform-specific CLI dependencies.
    raw = subprocess.check_output(['go', 'list', '-m', '-json', 'all'], cwd=root, text=True)
    decoder = json.JSONDecoder()
    modules = []
    while raw.strip():
        value, end = decoder.raw_decode(raw.lstrip())
        modules.append(value)
        raw = raw.lstrip()[end:]
    texts = []
    for module in modules:
        if module.get('Main'):
            continue
        directory = module.get('Dir')
        if not directory:
            raise RuntimeError('Download all modules before assembling notices: ' + module['Path'])
        path = Path(directory)
        candidates = [p for p in path.iterdir() if p.is_file() and (p.name.upper().startswith('LICENSE') or p.name.upper().startswith('NOTICE') or p.name.upper().startswith('COPYING'))]
        if not any(p.name.upper().startswith(('LICENSE', 'COPYING')) for p in candidates):
            raise RuntimeError('Missing upstream license: ' + module['Path'])
        texts.append('\n=== ' + module['Path'] + ' ' + module.get('Version', '') + ' ===\n')
        for source in sorted(candidates):
            texts.append('\n--- ' + source.name + ' ---\n' + source.read_text())
    goroot = subprocess.check_output(['go', 'env', 'GOROOT'], cwd=root, text=True).strip()
    texts.append('\n=== Go standard library ===\n' + (Path(goroot) / 'LICENSE').read_text())
    destination.write_text(''.join(texts))
