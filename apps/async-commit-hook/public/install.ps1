param([string]$Version = "1.0.0", [string]$InstallDir = "$env:LOCALAPPDATA\async-commit-hook\bin")
$ErrorActionPreference = 'Stop'
if ($Version -notmatch '^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$') { throw 'Version must be MAJOR.MINOR.PATCH' }
foreach ($tool in @('cosign', 'git')) { Get-Command $tool -ErrorAction Stop | Out-Null }
$arch = [System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString().ToLowerInvariant()
if ($arch -eq 'x64') { $arch = 'amd64' }
if ($arch -notin @('amd64', 'arm64')) { throw 'Unsupported Windows architecture' }
$asset = "ach-windows-$arch.zip"
$base = "https://github.com/delinoio/oss/releases/download/async-commit-hook@v$Version"
$identity = 'https://github.com/delinoio/oss/.github/workflows/release-async-commit-hook.yml@refs/heads/main'
$temporary = Join-Path ([System.IO.Path]::GetTempPath()) ([System.Guid]::NewGuid().ToString())
New-Item -ItemType Directory $temporary | Out-Null
try {
  foreach ($name in @($asset, "$asset.sigstore.json", 'SHA256SUMS', 'SHA256SUMS.sigstore.json')) {
    Invoke-WebRequest -UseBasicParsing -Uri "$base/$name" -OutFile (Join-Path $temporary $name)
  }
  foreach ($name in @($asset, 'SHA256SUMS')) {
    & cosign verify-blob --bundle (Join-Path $temporary "$name.sigstore.json") --certificate-identity $identity --certificate-oidc-issuer https://token.actions.githubusercontent.com (Join-Path $temporary $name)
    if ($LASTEXITCODE -ne 0) { throw 'Signature verification failed' }
  }
  $entries = @(Get-Content (Join-Path $temporary 'SHA256SUMS') | Where-Object { ($_ -split '\s+')[1] -ceq $asset })
  if ($entries.Count -ne 1) { throw 'Missing or duplicate archive checksum' }
  $expected = ($entries[0] -split '\s+')[0]
  if ((Get-FileHash (Join-Path $temporary $asset) -Algorithm SHA256).Hash.ToLowerInvariant() -cne $expected) { throw 'Checksum mismatch' }
  Add-Type -AssemblyName System.IO.Compression.FileSystem
  $zip = [System.IO.Compression.ZipFile]::OpenRead((Join-Path $temporary $asset))
  try {
    if ($zip.Entries.Count -ne 1 -or $zip.Entries[0].FullName -cne 'ach.exe') { throw 'Invalid archive layout' }
    [System.IO.Compression.ZipFileExtensions]::ExtractToFile($zip.Entries[0], (Join-Path $temporary 'ach.exe'))
  } finally { $zip.Dispose() }
  New-Item -ItemType Directory -Force $InstallDir | Out-Null
  $destination = Join-Path $InstallDir 'ach.exe'
  if (Test-Path $destination) { throw 'An installation exists. Use ach self-update after stopping active checks and servers.' }
  $staged = Join-Path $InstallDir ('.ach-install-' + [System.Guid]::NewGuid().ToString() + '.exe')
  try {
    Copy-Item (Join-Path $temporary 'ach.exe') $staged
    [System.IO.File]::Move($staged, $destination)
  } finally { if (Test-Path $staged) { Remove-Item $staged } }
  Write-Output "Installed ach $Version at $destination. Add $InstallDir to PATH and run ach init."
} finally { Remove-Item -Recurse -Force $temporary }
