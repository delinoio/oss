param(
  [string]$Version = "latest",
  [ValidateSet("package-manager", "direct")]
  [string]$Method = "direct",
  [string]$InstallDir = "$HOME\\.local\\bin"
)

$ErrorActionPreference = "Stop"

$Repo = "delinoio/oss"
$TagPrefix = "binpm@v"
$SupportedDirectTargets = "darwin/amd64 (macOS x64), darwin/arm64 (macOS arm64), linux/amd64 (Linux x64), linux/arm64 (Linux arm64), windows/amd64 (Windows x64), windows/arm64 (Windows arm64)"
$UnsupportedPlatformHint = "On Windows, use an x64/arm64 host or supported CI image for PowerShell direct install. On macOS/Linux x64 or arm64, use the POSIX installer. Otherwise use Homebrew or cargo-binstall where they support your host, or build binpm from source."

function Resolve-LatestTag {
  $releases = Invoke-RestMethod -Uri "https://api.github.com/repos/$Repo/releases?per_page=200"
  $match = $releases | Where-Object { $_.tag_name -like "$TagPrefix*" } | Select-Object -First 1
  if (-not $match) {
    throw "[install.binpm] failed to resolve latest tag"
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
    throw "[install.binpm] checksum entry not found for $AssetName"
  }

  $expectedHash = ($expected -split "\s+")[0].ToLowerInvariant()
  $actualHash = (Get-FileHash -Path $FilePath -Algorithm SHA256).Hash.ToLowerInvariant()
  if ($expectedHash -ne $actualHash) {
    throw "[install.binpm] checksum mismatch for $AssetName"
  }
}

function Get-DetectedOs {
  if ($env:BINPM_INSTALL_TEST_OS) {
    return $env:BINPM_INSTALL_TEST_OS.ToLowerInvariant()
  }

  if ([System.Runtime.InteropServices.RuntimeInformation]::IsOSPlatform([System.Runtime.InteropServices.OSPlatform]::Windows)) {
    return "windows"
  }
  if ([System.Runtime.InteropServices.RuntimeInformation]::IsOSPlatform([System.Runtime.InteropServices.OSPlatform]::Linux)) {
    return "linux"
  }
  if ([System.Runtime.InteropServices.RuntimeInformation]::IsOSPlatform([System.Runtime.InteropServices.OSPlatform]::OSX)) {
    return "darwin"
  }

  return [System.Runtime.InteropServices.RuntimeInformation]::OSDescription.ToLowerInvariant()
}

function New-UnsupportedDirectPlatformMessage {
  param(
    [string]$Os,
    [string]$Architecture
  )

  $artifactBoundary = if ($Os -eq "windows") {
    "no first-party binpm PowerShell direct installer artifact is published for this detected host"
  }
  else {
    "this PowerShell installer only installs Windows direct artifacts; use the POSIX installer for supported macOS/Linux hosts"
  }

  return @"
[install.binpm] unsupported host platform for direct installation: detected os=$Os, arch=$Architecture
[install.binpm] $artifactBoundary
[install.binpm] supported direct-install targets: $SupportedDirectTargets
[install.binpm] recommended alternatives: $UnsupportedPlatformHint
"@
}

function Get-DirectPlatform {
  $os = Get-DetectedOs
  $architecture = if ($env:BINPM_INSTALL_TEST_ARCHITECTURE) {
    $env:BINPM_INSTALL_TEST_ARCHITECTURE.ToLowerInvariant()
  }
  else {
    [System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString().ToLowerInvariant()
  }

  if ($os -ne "windows") {
    throw (New-UnsupportedDirectPlatformMessage -Os $os -Architecture $architecture)
  }

  $assetArch = switch ($architecture) {
    "x64" { "amd64" }
    "x86_64" { "amd64" }
    "amd64" { "amd64" }
    "arm64" { "arm64" }
    "aarch64" { "arm64" }
    "x86" {
      throw (New-UnsupportedDirectPlatformMessage -Os $os -Architecture $architecture)
    }
    "i386" {
      throw (New-UnsupportedDirectPlatformMessage -Os $os -Architecture $architecture)
    }
    "i686" {
      throw (New-UnsupportedDirectPlatformMessage -Os $os -Architecture $architecture)
    }
    "ia32" {
      throw (New-UnsupportedDirectPlatformMessage -Os $os -Architecture $architecture)
    }
    default {
      throw (New-UnsupportedDirectPlatformMessage -Os $os -Architecture $architecture)
    }
  }

  return @{
    Os = $os
    AssetArch = $assetArch
    Architecture = $architecture
  }
}

function Install-Direct {
  $platform = Get-DirectPlatform
  $tag = Resolve-Tag
  $baseUrl = "https://github.com/$Repo/releases/download/$tag"
  $assetArch = $platform.AssetArch
  $assetName = "binpm-windows-$assetArch.zip"

  $tmpDir = Join-Path ([System.IO.Path]::GetTempPath()) ("binpm-install-" + [System.Guid]::NewGuid().ToString("N"))
  New-Item -Path $tmpDir -ItemType Directory | Out-Null

  try {
    $assetPath = Join-Path $tmpDir $assetName
    $sumsPath = Join-Path $tmpDir "SHA256SUMS"

    Write-Host "[install.binpm] downloading $assetName"
    Invoke-WebRequest -Uri "$baseUrl/$assetName" -OutFile $assetPath -UseBasicParsing
    Invoke-WebRequest -Uri "$baseUrl/SHA256SUMS" -OutFile $sumsPath -UseBasicParsing

    Verify-Checksum -FilePath $assetPath -Sha256SumsPath $sumsPath -AssetName $assetName

    $extractDir = Join-Path $tmpDir "extract"
    Expand-Archive -Path $assetPath -DestinationPath $extractDir -Force

    New-Item -Path $InstallDir -ItemType Directory -Force | Out-Null
    $extractedBinary = Join-Path $extractDir "binpm.exe"
    if (-not (Test-Path -Path $extractedBinary)) {
      $extractedBinary = Join-Path $extractDir "binpm-windows-$assetArch.exe"
    }
    if (-not (Test-Path -Path $extractedBinary)) {
      throw "[install.binpm] extracted archive did not contain binpm.exe"
    }
    Copy-Item -Path $extractedBinary -Destination (Join-Path $InstallDir "binpm.exe") -Force

    Write-Host "[install.binpm] installed binpm.exe to $InstallDir"
  }
  finally {
    Remove-Item -Path $tmpDir -Recurse -Force -ErrorAction SilentlyContinue
  }
}

switch ($Method) {
  "package-manager" {
    # Compatibility shim: Windows users may pass package-manager from shared
    # install templates even though direct is the only supported Windows path.
    # Remove when all downstream automation selects the direct method on Windows.
    Write-Warning "[install.binpm] method=package-manager is not available on Windows and now maps to direct installation."
    Install-Direct
  }
  "direct" {
    Install-Direct
  }
}
