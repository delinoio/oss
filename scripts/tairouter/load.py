#!/usr/bin/env python3
"""Deterministic synthetic workload; measurements are observations, not SLOs."""
import argparse
from concurrent.futures import ThreadPoolExecutor
import json
import os
import platform
import resource
import subprocess
import time
import urllib.error
import urllib.request
from pathlib import Path


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--url', default='http://127.0.0.1:18090/v1/chat/completions')
    parser.add_argument('--requests', type=int, default=200)
    parser.add_argument('--concurrency', type=int, default=4)
    parser.add_argument('--output', required=True)
    parser.add_argument('--server-pid', type=int, help='Sample synthetic server CPU time and RSS with ps')
    parser.add_argument('--server-resources', default='not measured; use docker stats for Compose runs')
    args = parser.parse_args()
    if args.requests < 1 or args.concurrency < 1:
        parser.error('requests and concurrency must be positive')
    body = json.dumps({'model': 'fixture-model', 'messages': [{'role': 'user', 'content': 'synthetic fixed workload'}], 'stream': False}, sort_keys=True).encode()
    headers = {'Content-Type': 'application/json'}
    if os.environ.get('TAIROUTER_LOAD_KEY'):
        headers['Authorization'] = 'Bearer ' + os.environ['TAIROUTER_LOAD_KEY']
    # Never follow redirects with authorization or record response content.
    class NoRedirect(urllib.request.HTTPRedirectHandler):
        def redirect_request(self, req, fp, code, msg, headers, newurl):
            return None
    def request(index):
        start = time.perf_counter()
        try:
            opener = urllib.request.build_opener(NoRedirect())
            with opener.open(urllib.request.Request(args.url, body, headers), timeout=30) as response:
                response.read(1024 * 1024)
                status = response.status
        except urllib.error.HTTPError as error:
            status = error.code
            error.close()
        except (OSError, urllib.error.URLError):
            status = 0
        return (time.perf_counter() - start) * 1000, status
    def server_snapshot():
        if not args.server_pid:
            return None
        return subprocess.check_output(['ps', '-p', str(args.server_pid), '-o', 'time=', '-o', 'rss='], text=True).strip()
    server_before = server_snapshot()
    start = time.perf_counter()
    with ThreadPoolExecutor(max_workers=args.concurrency) as pool:
        results = list(pool.map(request, range(args.requests)))
    elapsed = time.perf_counter() - start
    latencies = sorted(value for value, _ in results)
    counts = {}
    for _, status in results:
        counts[str(status)] = counts.get(str(status), 0) + 1
    usage = resource.getrusage(resource.RUSAGE_SELF)
    def percentile(p):
        return latencies[min(len(latencies)-1, int((len(latencies)-1)*p))]
    report = {'schema': 1, 'revision': subprocess.check_output(['git', 'rev-parse', 'HEAD'], text=True).strip(),
              'workload': {'seed': 0, 'requests': args.requests, 'concurrency': args.concurrency, 'model': 'fixture-model', 'stream': False},
              'hardware': {'system': platform.system(), 'machine': platform.machine(), 'cpu_count': os.cpu_count(), 'cpu': platform.processor()},
              'resources': {'harness_user_cpu_seconds': usage.ru_utime, 'harness_system_cpu_seconds': usage.ru_stime, 'harness_max_rss': usage.ru_maxrss, 'rss_unit': 'bytes' if platform.system() == 'Darwin' else 'KiB', 'server': ('ps sampling; no container resource limits supplied' if args.server_pid and args.server_resources.startswith('not measured') else args.server_resources), 'server_cpu_time_and_rss_kib_before': server_before, 'server_cpu_time_and_rss_kib_after': server_snapshot()},
              'results': {'duration_seconds': elapsed, 'requests_per_second': args.requests/elapsed, 'latency_ms': {'p50': percentile(.5), 'p95': percentile(.95), 'p99': percentile(.99), 'max': max(latencies)}, 'statuses': counts, 'errors': sum(1 for _, s in results if s < 200 or s >= 300)},
              'acceptance': 'synthetic workload only; no live provider, native OS or SLO acceptance'}
    Path(args.output).write_text(json.dumps(report, indent=2) + '\n')
    print(json.dumps(report, indent=2))
    return 1 if report['results']['errors'] else 0


if __name__ == '__main__':
    raise SystemExit(main())
