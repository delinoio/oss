param([string]$Version = "", [string]$InstallDir = "$env:LOCALAPPDATA\async-commit-hook\bin")
$ErrorActionPreference = 'Stop'

function Get-LatestPublishedVersion {
  $headers = @{
    Accept = 'application/vnd.github+json'
    'X-GitHub-Api-Version' = '2022-11-28'
  }
  $releases = @()
  for ($page = 1; ; $page++) {
    $batch = @(Invoke-RestMethod -Headers $headers -Uri "https://api.github.com/repos/delinoio/oss/releases?per_page=100&page=$page")
    if ($batch.Count -eq 0) { break }
    $releases += $batch
  }
  $pattern = '^async-commit-hook@v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$'
  $candidates = @(
    foreach ($release in $releases) {
      if ($release.draft -or $release.prerelease) { continue }
      if ($release.tag_name -notmatch $pattern) { continue }
      $version = "$($matches[1]).$($matches[2]).$($matches[3])"
      [pscustomobject]@{ Version = $version; Parsed = [version]::Parse($version) }
    }
  )
  $selected = $candidates | Sort-Object Parsed -Descending | Select-Object -First 1
  if ($null -eq $selected) { throw 'Could not determine the latest published async-commit-hook release.' }
  $selected.Version
}

if ([string]::IsNullOrEmpty($Version)) {
  $Version = Get-LatestPublishedVersion
  $base = "https://github.com/delinoio/oss/releases/download/async-commit-hook@v$Version"
  $displayVersion = $Version
} else {
  if ($Version -notmatch '^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$') { throw 'Version must be MAJOR.MINOR.PATCH' }
  $base = "https://github.com/delinoio/oss/releases/download/async-commit-hook@v$Version"
  $displayVersion = $Version
}
foreach ($tool in @('cosign', 'git')) { Get-Command $tool -ErrorAction Stop | Out-Null }
$arch = [System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString().ToLowerInvariant()
if ($arch -eq 'x64') { $arch = 'amd64' }
if ($arch -notin @('amd64', 'arm64')) { throw 'Unsupported Windows architecture' }
$asset = "ach-windows-$arch.zip"
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
  Write-Output "Installed ach $displayVersion at $destination. Add $InstallDir to PATH and run ach init."
} finally { Remove-Item -Recurse -Force $temporary }
