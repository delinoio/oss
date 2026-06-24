param(
  [string]$Version = "latest",
  [ValidateSet("package-manager", "direct")]
  [string]$Method = "direct",
  [string]$InstallDir = "$HOME\\.local\\bin"
)

$ErrorActionPreference = "Stop"

$Repo = "delinoio/oss"
$TagPrefix = "nodeup@v"
$SupportedPlatforms = "macOS x64, macOS arm64, Linux x64, Linux arm64, Windows x64, and Windows arm64"
$UnsupportedPlatformHint = "Use an x64/arm64 host or a supported CI image: macOS x64/arm64, Linux x64/arm64, or Windows x64/arm64."

function Resolve-LatestTag {
  $releases = Invoke-RestMethod -Uri "https://api.github.com/repos/$Repo/releases?per_page=200"
  $match = $releases | Where-Object { $_.tag_name -like "$TagPrefix*" } | Select-Object -First 1
  if (-not $match) {
    throw "[install.nodeup] failed to resolve latest tag"
  }

  return $match.tag_name
}

function Resolve-Tag {
  if ($Version -eq "latest") {
    return Resolve-LatestTag
  }

  return "$TagPrefix$Version"
}

function Verify-Checksum {
  param(
    [string]$FilePath,
    [string]$Sha256SumsPath,
    [string]$AssetName
  )

  $expected = Get-Content -Path $Sha256SumsPath | Where-Object { $_ -match "\s$([regex]::Escape($AssetName))$" } | Select-Object -First 1
  if (-not $expected) {
    throw "[install.nodeup] checksum entry not found for $AssetName"
  }

  $expectedHash = ($expected -split "\s+")[0].ToLowerInvariant()
  $actualHash = (Get-FileHash -Path $FilePath -Algorithm SHA256).Hash.ToLowerInvariant()
  if ($expectedHash -ne $actualHash) {
    throw "[install.nodeup] checksum mismatch for $AssetName"
  }
}

function Get-DirectPlatform {
  $architecture = if ($env:NODEUP_INSTALL_TEST_ARCHITECTURE) {
    $env:NODEUP_INSTALL_TEST_ARCHITECTURE.ToLowerInvariant()
  }
  else {
    [System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString().ToLowerInvariant()
  }

  $assetArch = switch ($architecture) {
    "x64" { "amd64" }
    "x86_64" { "amd64" }
    "amd64" { "amd64" }
    "arm64" { "arm64" }
    "aarch64" { "arm64" }
    "x86" {
      throw "[install.nodeup] unsupported host platform for direct installation: os=windows, arch=$architecture. Supported platforms: $SupportedPlatforms; x86 hosts are unsupported. Hint: $UnsupportedPlatformHint"
    }
    "i386" {
      throw "[install.nodeup] unsupported host platform for direct installation: os=windows, arch=$architecture. Supported platforms: $SupportedPlatforms; x86 hosts are unsupported. Hint: $UnsupportedPlatformHint"
    }
    "i686" {
      throw "[install.nodeup] unsupported host platform for direct installation: os=windows, arch=$architecture. Supported platforms: $SupportedPlatforms; x86 hosts are unsupported. Hint: $UnsupportedPlatformHint"
    }
    "ia32" {
      throw "[install.nodeup] unsupported host platform for direct installation: os=windows, arch=$architecture. Supported platforms: $SupportedPlatforms; x86 hosts are unsupported. Hint: $UnsupportedPlatformHint"
    }
    default {
      throw "[install.nodeup] unsupported host platform for direct installation: os=windows, arch=$architecture. Supported platforms: $SupportedPlatforms; x86 hosts are unsupported. Hint: $UnsupportedPlatformHint"
    }
  }

  return @{
    AssetArch = $assetArch
    Architecture = $architecture
  }
}

function Install-Direct {
  $platform = Get-DirectPlatform
  $tag = Resolve-Tag
  $baseUrl = "https://github.com/$Repo/releases/download/$tag"
  $assetArch = $platform.AssetArch
  $assetName = "nodeup-windows-$assetArch.zip"

  $tmpDir = Join-Path ([System.IO.Path]::GetTempPath()) ("nodeup-install-" + [System.Guid]::NewGuid().ToString("N"))
  New-Item -Path $tmpDir -ItemType Directory | Out-Null

  try {
    $assetPath = Join-Path $tmpDir $assetName
    $sumsPath = Join-Path $tmpDir "SHA256SUMS"

    Write-Host "[install.nodeup] downloading $assetName"
    Invoke-WebRequest -Uri "$baseUrl/$assetName" -OutFile $assetPath -UseBasicParsing
    Invoke-WebRequest -Uri "$baseUrl/SHA256SUMS" -OutFile $sumsPath -UseBasicParsing

    Verify-Checksum -FilePath $assetPath -Sha256SumsPath $sumsPath -AssetName $assetName

    $extractDir = Join-Path $tmpDir "extract"
    Expand-Archive -Path $assetPath -DestinationPath $extractDir -Force

    New-Item -Path $InstallDir -ItemType Directory -Force | Out-Null
    $extractedBinary = Join-Path $extractDir "nodeup.exe"
    if (-not (Test-Path -Path $extractedBinary)) {
      $extractedBinary = Join-Path $extractDir "nodeup-windows-$assetArch.exe"
    }
    if (-not (Test-Path -Path $extractedBinary)) {
      throw "[install.nodeup] extracted archive did not contain nodeup.exe"
    }
    Copy-Item -Path $extractedBinary -Destination (Join-Path $InstallDir "nodeup.exe") -Force

    Write-Host "[install.nodeup] installed nodeup.exe to $InstallDir"
  }
  finally {
    Remove-Item -Path $tmpDir -Recurse -Force -ErrorAction SilentlyContinue
  }
}

switch ($Method) {
  "package-manager" {
    # Compatibility shim: keep accepting the legacy package-manager flag until downstream
    # automation and docs stop sending it for Windows installs.
    Write-Warning "[install.nodeup] method=package-manager is deprecated on Windows and now maps to direct installation."
    Install-Direct
  }
  "direct" {
    Install-Direct
  }
}
