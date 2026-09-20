#!/usr/bin/env python3
"""Closed Runlens artifact inventory and fail-closed native release evidence gate."""
import argparse
import hashlib
import io
import json
import os
from pathlib import Path
import platform
import re
import subprocess
import tarfile
import tomllib
import zipfile

ROOT = Path(__file__).resolve().parents[2]
SOURCE = ROOT / 'crates/runlens'
PLATFORMS = {
    'darwin-amd64': 'x86_64-apple-darwin',
    'darwin-arm64': 'aarch64-apple-darwin',
    'linux-amd64': 'x86_64-unknown-linux-gnu',
    'linux-arm64': 'aarch64-unknown-linux-gnu',
    'windows-amd64': 'x86_64-pc-windows-msvc',
    'windows-arm64': 'aarch64-pc-windows-msvc',
}

def asset_name(key):
    if key not in PLATFORMS:
        raise ValueError('unknown platform')
    return f'runlens-{key}.' + ('zip' if key.startswith('windows') else 'tar.gz')

def digest(path):
    with path.open('rb') as handle:
        return hashlib.file_digest(handle, 'sha256').hexdigest()

def source_version():
    return tomllib.loads((SOURCE / "Cargo.toml").read_text())["package"]["version"]

def version(value):
    if not re.fullmatch(r'(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)', value):
        raise ValueError('expected stable X.Y.Z version')
    if value != source_version():
        raise ValueError('version differs from source')
    return value

def host_key():
    system = {'Darwin': 'darwin', 'Linux': 'linux', 'Windows': 'windows'}[platform.system()]
    arch = {'x86_64': 'amd64', 'AMD64': 'amd64', 'arm64': 'arm64', 'aarch64': 'arm64', 'ARM64': 'arm64'}[platform.machine()]
    return f'{system}-{arch}'

def minimum_os():
    if platform.system() == 'Darwin':
        return platform.mac_ver()[0].split('.')[0] == '13'
    if platform.system() == 'Windows':
        # Windows 11 also reports NT 10.0. Require the Windows 10 22H2 build.
        return platform.version().split('.')[:3] == ['10', '0', '19045']
    release = platform.freedesktop_os_release()
    return release.get('ID') == 'ubuntu' and release.get('VERSION_ID') == '22.04'

def windows_native_architecture():
    # Query native host identity even when Python itself runs under WOW64.
    # https://learn.microsoft.com/windows/win32/api/wow64apiset/nf-wow64apiset-iswow64process2
    import ctypes
    from ctypes import wintypes
    kernel = ctypes.WinDLL('kernel32', use_last_error=True)
    kernel.GetCurrentProcess.restype = wintypes.HANDLE
    kernel.IsWow64Process2.argtypes = [wintypes.HANDLE, ctypes.POINTER(wintypes.USHORT), ctypes.POINTER(wintypes.USHORT)]
    kernel.IsWow64Process2.restype = wintypes.BOOL
    process, native = wintypes.USHORT(), wintypes.USHORT()
    if not kernel.IsWow64Process2(kernel.GetCurrentProcess(), ctypes.byref(process), ctypes.byref(native)):
        raise ValueError('cannot establish native Windows architecture')
    architecture = {0x8664: 'amd64', 0xAA64: 'arm64'}.get(native.value)
    if architecture is None:
        raise ValueError('unsupported native Windows architecture')
    return architecture

def install_host(args):
    if args.platform not in PLATFORMS or host_key() != args.platform:
        raise ValueError('installer runner differs from its expected platform')
    if platform.system() == 'Windows' and args.platform != f'windows-{windows_native_architecture()}':
        raise ValueError('installer validation must use the native Windows architecture')
    if platform.system() == 'Darwin':
        translated = subprocess.check_output(['sysctl', '-in', 'sysctl.proc_translated'], text=True).strip()
        if translated not in ('', '0'):
            raise ValueError('installer validation cannot run under Rosetta')
    if not minimum_os():
        raise ValueError('installer validation requires the exact minimum OS')
    print(json.dumps({'platform': args.platform, 'os_version': platform.platform(), 'minimum_os': True}))

def licenses():
    metadata = json.loads(subprocess.check_output(['cargo', 'metadata', '--locked', '--format-version', '1'], cwd=SOURCE))
    sections = []
    for package in sorted(metadata['packages'], key=lambda item: item['id']):
        directory = Path(package['manifest_path']).parent
        candidates = sorted(set(directory.glob('LICENSE*')) | set(directory.glob('COPYING*')) | set(directory.glob('NOTICE*')))
        if package.get('license_file'):
            candidates.append(directory / package['license_file'])
        texts = [file.read_text(errors='replace') for file in candidates if file.is_file()]
        sections.append(f"{package['name']} {package['version']} ({package.get('license') or 'see notices'})\n" + '\n'.join(texts))
    for name in ['UPSTREAM-LICENSE', 'vendor/fspy_detours_sys/detours/LICENSE.md']:
        file = SOURCE / name
        if file.exists():
            sections.append(file.read_text())
    return '\n\n'.join(sections).encode()

def package(args):
    version(args.version)
    if host_key() != args.platform:
        raise ValueError('cross compilation is not native validation')
    extension = '.exe' if args.platform.startswith('windows') else ''
    binary = SOURCE / 'target' / PLATFORMS[args.platform] / 'release' / ('runlens' + extension)
    if subprocess.check_output([binary, '--version'], text=True).strip() != f'runlens {args.version}':
        raise ValueError('binary version differs from source')
    args.directory.mkdir(parents=True, exist_ok=True)
    archive = args.directory / asset_name(args.platform)
    if archive.exists():
        raise ValueError('archive already exists')
    files = [('runlens' + extension, binary.read_bytes(), 0o755), ('LICENSES.txt', licenses(), 0o644)]
    if extension:
        with zipfile.ZipFile(archive, 'x', compression=zipfile.ZIP_DEFLATED) as output:
            for name, data, mode in files:
                info = zipfile.ZipInfo(name, date_time=(1980, 1, 1, 0, 0, 0))
                info.external_attr = (0o100000 | mode) << 16
                output.writestr(info, data)
    else:
        import gzip
        with archive.open('xb') as handle, gzip.GzipFile(fileobj=handle, mode='wb', filename='', mtime=0) as gz, tarfile.open(fileobj=gz, mode='w') as output:
            for name, data, mode in files:
                info = tarfile.TarInfo(name)
                info.size, info.mode, info.mtime = len(data), mode, 0
                output.addfile(info, io.BytesIO(data))
    print(archive.name)

def evidence(args):
    version(args.version)
    if host_key() != args.platform:
        raise ValueError('evidence must come from the native architecture')
    archive = args.directory / asset_name(args.platform)
    # Called only after native tests and the installer fixture pass in the owning workflow.
    payload = {
        'schema_version': 1, 'version': args.version, 'platform': args.platform,
        'commit': subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=ROOT, text=True).strip(),
        'archive': archive.name, 'sha256': digest(archive),
        'os_version': platform.platform(), 'minimum_os': minimum_os(),
        'native_execution': True, 'installer_fixture': True,
        'signed_installer': False, 'run_id': os.environ.get('GITHUB_RUN_ID'),
    }
    path = args.directory / f'evidence-{args.platform}.json'
    with path.open('x') as handle:
        json.dump(payload, handle, indent=2)

def inventory(directory, release_version):
    version(release_version)
    names = sorted(asset_name(key) for key in PLATFORMS)
    actual = sorted(file.name for file in directory.iterdir() if file.name.endswith(('.zip', '.tar.gz')))
    if names != actual:
        raise ValueError('missing, duplicate or unexpected platform archive')
    for key in PLATFORMS:
        file = directory / asset_name(key)
        if file.is_symlink() or file.stat().st_size == 0:
            raise ValueError('invalid archive file')
        binary = 'runlens.exe' if key.startswith('windows') else 'runlens'
        if binary.endswith('.exe'):
            with zipfile.ZipFile(file) as archive:
                if sorted(archive.namelist()) != ['LICENSES.txt', binary]:
                    raise ValueError('invalid ZIP inventory')
                if any(entry.is_dir() or (entry.external_attr >> 16) & 0o170000 == 0o120000 for entry in archive.infolist()):
                    raise ValueError('ZIP entries must be regular files')
        else:
            with tarfile.open(file, 'r:gz') as archive:
                entries = archive.getmembers()
                if sorted(entry.name for entry in entries) != ['LICENSES.txt', binary] or any(not entry.isfile() for entry in entries):
                    raise ValueError('invalid tar inventory')
    return names

def collect(args):
    names = inventory(args.directory, args.version)
    checksums = ''.join(f'{digest(args.directory / name)}  {name}\n' for name in names)
    path = args.directory / 'SHA256SUMS'
    with path.open('x') as handle:
        handle.write(checksums)

def readiness(args):
    inventory(args.directory, args.version)
    reasons = []
    expected_commit = subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=ROOT, text=True).strip()
    for key in PLATFORMS:
        file = args.directory / f'evidence-{key}.json'
        try:
            proof = json.loads(file.read_text())
            valid = (proof['schema_version'] == 1 and proof['version'] == args.version and proof['platform'] == key
                     and proof['commit'] == expected_commit and proof['archive'] == asset_name(key)
                     and proof['sha256'] == digest(args.directory / asset_name(key))
                     and proof['native_execution'] is True and proof['installer_fixture'] is True)
            if not valid:
                reasons.append(f'{key}: invalid native evidence')
            if proof['minimum_os'] is not True:
                reasons.append(f'{key}: minimum OS validation missing')
        except (KeyError, OSError, ValueError):
            reasons.append(f'{key}: evidence missing')
    result = {'release': f'runlens@v{args.version}', 'ready_for_signing': not reasons, 'blockers': reasons}
    print(json.dumps(result, indent=2))
    if reasons and not args.dry_run:
        raise ValueError('release readiness failed')

def homebrew(args):
    inventory(args.directory, args.version)
    command = ['bash', str(ROOT / 'scripts/release/update-homebrew.sh'), '--project', 'runlens', '--version', args.version]
    for key in PLATFORMS:
        if key.startswith('windows'):
            continue
        command += [f'--{key}-url', f'https://github.com/delinoio/oss/releases/download/runlens@v{args.version}/{asset_name(key)}',
                    f'--{key}-sha256', digest(args.directory / asset_name(key))]
    if args.dry_run:
        command += ['--dry-run']
    subprocess.run(command, check=True)

def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('command', choices=['source-version', 'install-host', 'package', 'evidence', 'collect', 'readiness', 'homebrew'])
    parser.add_argument('--directory', type=Path)
    parser.add_argument('--version')
    parser.add_argument('--platform', choices=PLATFORMS)
    parser.add_argument('--dry-run', action='store_true')
    args = parser.parse_args()
    try:
        if args.command == 'source-version':
            print(version(source_version()))
            return
        if args.command == 'install-host':
            install_host(args)
            return
        if args.directory is None or args.version is None:
            parser.error('--directory and --version are required for artifact commands')
        globals()[args.command](args)
    except (ValueError, OSError, subprocess.CalledProcessError) as error:
        parser.exit(1, f'runlens release validation: {error}\n')

if __name__ == '__main__':
    main()
