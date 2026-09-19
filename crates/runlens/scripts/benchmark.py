#!/usr/bin/env python3
"""Reproducible local worktree/overhead benchmark. Prints metadata JSON only."""
import argparse
import hashlib
import json
from pathlib import Path
import platform
import statistics
import subprocess
import tempfile
import time

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--runlens', required=True, type=Path)
parser.add_argument('--fixture', required=True, type=Path)
parser.add_argument('--sizes', default='1000,10000')
parser.add_argument('--samples', type=int, default=5)
parser.add_argument('--memory-bytes', type=int, default=256 * 1024 * 1024)
parser.add_argument('--total-bytes', type=int, default=1024 * 1024 * 1024)
parser.add_argument('--expect-incomplete', action='store_true')
args = parser.parse_args()
if not 1 <= args.samples <= 100:
    parser.error('samples must be between 1 and 100')
args.runlens, args.fixture = args.runlens.resolve(), args.fixture.resolve()
sizes = [int(value) for value in args.sizes.split(',')]
if not sizes or any(size < 1 or size > 1_000_000 for size in sizes):
    parser.error('sizes must be between 1 and 1000000')
result = {'schema_version': 1, 'os': platform.platform(), 'architecture': platform.machine(),
          'runlens_version': subprocess.check_output([args.runlens, '--version'], text=True).strip(),
          'runlens_sha256': hashlib.sha256(args.runlens.read_bytes()).hexdigest(),
          'memory_bytes': args.memory_bytes, 'total_bytes': args.total_bytes, 'cases': []}
with tempfile.TemporaryDirectory(prefix='runlens-benchmark-') as temporary:
    for size in sizes:
        root = Path(temporary) / f'case-{size}'
        inputs = root / 'inputs'
        inputs.mkdir(parents=True)
        body = b'Runlens deterministic benchmark\n' * 32
        for index in range(size):
            (inputs / f'{index:08}.txt').write_bytes(body)
        (root / 'runlens.toml').write_text(f'schema_version=1\n[limits]\nmemory_bytes={args.memory_bytes}\ntotal_bytes={args.total_bytes}\n')
        samples = {'baseline_seconds': [], 'traced_seconds': [], 'traced_exit_codes': []}
        # Fixed input bytes and a warm-up make the procedure repeatable. OS caches
        # are not purged: the output describes a warm-cache local measurement.
        subprocess.run([args.fixture, 'benchmark'], cwd=root, check=True, stdin=subprocess.DEVNULL)
        for _ in range(args.samples):
            for traced in [False, True]:
                command = [str(args.fixture), 'benchmark']
                if traced:
                    command = [str(args.runlens), '--log-level', 'off', 'run', '--', *command]
                started = time.perf_counter()
                execution = subprocess.run(command, cwd=root, stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
                elapsed = time.perf_counter() - started
                if execution.returncode != (4 if traced and args.expect_incomplete else 0):
                    raise RuntimeError(f'benchmark execution failed with status {execution.returncode}')
                samples['traced_seconds' if traced else 'baseline_seconds'].append(elapsed)
                if traced:
                    samples['traced_exit_codes'].append(execution.returncode)
        result['cases'].append({'files': size, 'input_bytes': size * len(body), **samples,
                                'baseline_median_seconds': statistics.median(samples['baseline_seconds']),
                                'traced_median_seconds': statistics.median(samples['traced_seconds'])})
print(json.dumps(result, indent=2))
