"""Import the immutable runtime dependency closure before applying patches.

Maintainers run this only into an empty vendor directory; never overwrite local
correctness patches. Sources are obtained via the authenticated GitHub CLI.
"""
import hashlib
import json
import pathlib
import shutil
import subprocess
import tarfile
import tempfile
import tomllib

REVISION = '13aa80a0dac698023ce68ba16497b16e5330600b'
DETOURS_REVISION = '9764cebcb1a75940e68fa83d6730ffaf0f669401'
ROOT = pathlib.Path(__file__).resolve().parents[1]
if (ROOT / 'vendor').exists():
    raise SystemExit('Refusing to overwrite vendored patches')
with tempfile.TemporaryDirectory(prefix='runlens-upstream-') as directory:
    stage = pathlib.Path(directory)
    archive = stage / 'source.tar.gz'
    with archive.open('wb') as output:
        subprocess.run(['gh', 'api', f'repos/voidzero-dev/vite-task/tarball/{REVISION}'], stdout=output, check=True)
    with tarfile.open(archive) as source:
        source.extractall(stage, filter='data')
    upstream = next(path for path in stage.iterdir() if path.is_dir())
    dependencies = tomllib.loads((upstream / 'Cargo.toml').read_text())['workspace']['dependencies']
    selected, pending = set(), ['fspy']
    while pending:
        name = pending.pop()
        if name in selected:
            continue
        selected.add(name)
        package = tomllib.loads((upstream / 'crates' / name / 'Cargo.toml').read_text())
        sections = [package.get('dependencies', {}), package.get('build-dependencies', {})]
        for target in package.get('target', {}).values():
            sections.extend([target.get('dependencies', {}), target.get('build-dependencies', {})])
        for section in sections:
            for key in section:
                entry = dependencies.get(key)
                if isinstance(entry, dict) and 'path' in entry:
                    pending.append(key)
    original_hashes = {}
    for name in sorted(selected):
        destination = ROOT / 'vendor' / name
        shutil.copytree(upstream / 'crates' / name, destination, ignore=shutil.ignore_patterns('tests', 'examples', 'benches', '.clippy.toml'))
        for file in destination.rglob('*'):
            if file.is_file():
                original_hashes[str(file.relative_to(ROOT))] = hashlib.sha256(file.read_bytes()).hexdigest()
        lines, skip = [], False
        for line in (destination / 'Cargo.toml').read_text().splitlines():
            if line.startswith('['):
                skip = 'dev-dependencies' in line or any(line.startswith(prefix) for prefix in ['[[test', '[[bench', '[[example'])
            if not skip:
                lines.append(line)
        (destination / 'Cargo.toml').write_text('\n'.join(lines) + '\n')
    shutil.copy(upstream / 'LICENSE', ROOT / 'UPSTREAM-LICENSE')
    shutil.copy(upstream / 'Cargo.lock', ROOT / 'Cargo.lock')
    manifest = (upstream / 'Cargo.toml').read_text()
    inheritance = manifest[manifest.index('[workspace.package]'):]
    if '\n[profile.' in inheritance:
        inheritance = inheritance[:inheritance.index('\n[profile.')]
    inheritance = inheritance.replace('path = "crates/', 'path = "vendor/')
    (ROOT / 'Cargo.toml').write_text('''[workspace]
resolver = "3"
members = [".", "vendor/*"]

[package]
name = "runlens"
version = "0.1.0"
edition = "2024"
license = "MIT"
publish = false

[dependencies]
fspy = { path = "vendor/fspy" }
clap = { version = "4.5.53", features = ["derive"] }
serde = { version = "1.0.219", features = ["derive"] }
serde_json = "1.0.140"
toml = "1.0.0"
thiserror = "2"
tokio = { version = "1.48.0", features = ["rt-multi-thread", "macros", "signal", "time", "process", "io-util"] }
tokio-util = "0.7.17"
tracing = "0.1.43"
tracing-subscriber = { version = "0.3.19", features = ["env-filter"] }
uuid = { version = "1.18.1", features = ["v7", "serde"] }
sha2 = "0.11.0"
hex = "0.4.3"
globset = "0.4.18"
tempfile = "3.14.0"
regex = "1.11.3"
walkdir = "2.5.0"
libc = "0.2.185"

[target.'cfg(windows)'.dependencies]
windows-sys = { version = "0.61", features = ["Win32_Foundation", "Win32_System_JobObjects", "Win32_System_Threading", "Win32_System_Console", "Win32_Security"] }

''' + inheritance)
    (ROOT / 'rust-toolchain.toml').write_text('[toolchain]\nchannel = "nightly-2026-08-02"\nprofile = "minimal"\ncomponents = ["rustfmt", "clippy"]\n')
    (ROOT / '.cargo').mkdir(exist_ok=True)
    (ROOT / '.cargo/config.toml').write_text('[unstable]\nbindeps = true\n')
    (ROOT / 'src').mkdir(exist_ok=True)
    (ROOT / 'src/main.rs').write_text('fn main() {}\n')
    with archive.open('wb') as output:
        subprocess.run(['gh', 'api', f'repos/microsoft/Detours/tarball/{DETOURS_REVISION}'], stdout=output, check=True)
    detours = stage / 'detours'
    detours.mkdir()
    with tarfile.open(archive) as source:
        source.extractall(detours, filter='data')
    source = next(detours.iterdir())
    destination = ROOT / 'vendor/fspy_detours_sys/detours'
    destination.mkdir(exist_ok=True)
    shutil.copytree(source / 'src', destination / 'src')
    for name in ['LICENSE.md', 'LICENSE', 'README.md']:
        if (source / name).exists():
            shutil.copy(source / name, destination / name)
    (ROOT / 'vendor-provenance.json').write_text(json.dumps({'repository': 'https://github.com/voidzero-dev/vite-task', 'revision': REVISION, 'license': 'MIT', 'crates': sorted(selected), 'original_sha256': original_hashes, 'detours': {'repository': 'https://github.com/microsoft/Detours', 'revision': DETOURS_REVISION, 'license': 'MIT'}}, indent=2) + '\n')
    print('Vendored:', ', '.join(sorted(selected)))
