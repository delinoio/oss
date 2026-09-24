param(
  [string]$Version = "latest",
  [string]$InstallDir = "$HOME\.local\bin",
  [string]$SourceDir = ""
)

$ErrorActionPreference = "Stop"
if ($Version -eq "latest") {
  if ($SourceDir) { throw "[install.pnport] SourceDir requires an exact version" }
  $versions = @()
  $page = 1
  while ($true) {
    $batch = Invoke-RestMethod -Uri "https://api.github.com/repos/delinoio/oss/releases?per_page=100&page=$page"
    if ($null -eq $batch -or $batch.Count -eq 0) { break }
    foreach ($release in $batch) {
      if ($release.tag_name -cmatch '^pnport@v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$') {
        $versions += $release.tag_name.Substring(8)
      }
    }
    $page += 1
  }
  if ($versions.Count -eq 0) { throw "[install.pnport] no published pnport version" }
  $Version = $versions | Sort-Object { [version]$_ } -Descending | Select-Object -First 1
}
if ($Version -cnotmatch '^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$') { throw "[install.pnport] exact stable version required" }
if (-not $InstallDir) { throw "[install.pnport] install directory required" }
$arch = [System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString().ToLowerInvariant()
if ($arch -notin @("x64", "arm64")) { throw "[install.pnport] unsupported architecture" }
$asset = "pnport-win32-$arch-msvc.tar.gz"
$temporary = Join-Path ([System.IO.Path]::GetTempPath()) ("pnport-install-" + [guid]::NewGuid().ToString("N"))
New-Item -Path $temporary -ItemType Directory | Out-Null
$stage = $null
try {
  $archive = Join-Path $temporary $asset
  $sums = Join-Path $temporary "SHA256SUMS"
  if ($SourceDir) {
    Copy-Item -LiteralPath (Join-Path $SourceDir $asset) -Destination $archive
    Copy-Item -LiteralPath (Join-Path $SourceDir "SHA256SUMS") -Destination $sums
  } else {
    $base = "https://github.com/delinoio/oss/releases/download/pnport@v$Version"
    Invoke-WebRequest -Uri "$base/$asset" -OutFile $archive
    Invoke-WebRequest -Uri "$base/SHA256SUMS" -OutFile $sums
  }
  $entries = @(Get-Content -LiteralPath $sums | Where-Object { $_ -cmatch "^([a-f0-9]{64})  $([regex]::Escape($asset))$" })
  if ($entries.Count -ne 1) { throw "[install.pnport] missing or duplicate archive checksum" }
  $expected = $entries[0].Substring(0, 64)
  if ((Get-FileHash -LiteralPath $archive -Algorithm SHA256).Hash.ToLowerInvariant() -cne $expected) { throw "[install.pnport] checksum mismatch" }
  $inventory = @(tar.exe -tzf $archive)
  if ($LASTEXITCODE -ne 0 -or $inventory.Count -ne 3 -or -not ($inventory -ccontains "pnport.exe") -or -not ($inventory -ccontains "pnport_preload.dll") -or -not ($inventory -ccontains "LICENSE")) { throw "[install.pnport] invalid native archive inventory" }
  tar.exe -xzf $archive -C $temporary
  if ($LASTEXITCODE -ne 0) { throw "[install.pnport] archive extraction failed" }
  $executable = Join-Path $temporary "pnport.exe"
  $preload = Join-Path $temporary "pnport_preload.dll"
  if (-not (Test-Path -LiteralPath $executable -PathType Leaf) -or -not (Test-Path -LiteralPath $preload -PathType Leaf)) { throw "[install.pnport] incomplete native archive" }
  if ((& $executable --version) -cne "pnport $Version") { throw "[install.pnport] executable version mismatch" }

  $versions = Join-Path $InstallDir ".pnport\versions"
  New-Item -Path $versions -ItemType Directory -Force | Out-Null
  $destination = Join-Path $versions $Version
  $stage = Join-Path $versions (".stage-" + [guid]::NewGuid().ToString("N"))
  New-Item -Path $stage -ItemType Directory | Out-Null
  Copy-Item -LiteralPath $executable,$preload,(Join-Path $temporary "LICENSE") -Destination $stage
  if (Test-Path -LiteralPath $destination) {
    foreach ($name in @("pnport.exe", "pnport_preload.dll", "LICENSE")) {
      if ((Get-FileHash -LiteralPath (Join-Path $stage $name) -Algorithm SHA256).Hash -cne (Get-FileHash -LiteralPath (Join-Path $destination $name) -Algorithm SHA256).Hash) { throw "[install.pnport] conflicting installed version" }
    }
  } else {
    Move-Item -LiteralPath $stage -Destination $destination
    $stage = $null
  }
  $launcher = Join-Path $InstallDir "pnport.cmd"
  $pending = Join-Path $InstallDir (".pnport-" + [guid]::NewGuid().ToString("N") + ".cmd")
  $backup = Join-Path $InstallDir (".pnport-" + [guid]::NewGuid().ToString("N") + ".previous.cmd")
  $body = "@echo off`r`n`"%~dp0.pnport\versions\$Version\pnport.exe`" %*`r`nexit /b %ERRORLEVEL%`r`n"
  [System.IO.File]::WriteAllText($pending, $body, [System.Text.UTF8Encoding]::new($false))
  try {
    if (Test-Path -LiteralPath $launcher) {
      [System.IO.File]::Replace($pending, $launcher, $backup)
    } else {
      [System.IO.File]::Move($pending, $launcher)
    }
  } finally {
    Remove-Item -LiteralPath $pending,$backup -Force -ErrorAction SilentlyContinue
  }
  Write-Host "[install.pnport] installed pnport $Version in $InstallDir"
} finally {
  if ($stage) { Remove-Item -LiteralPath $stage -Recurse -Force -ErrorAction SilentlyContinue }
  Remove-Item -LiteralPath $temporary -Recurse -Force -ErrorAction SilentlyContinue
}
