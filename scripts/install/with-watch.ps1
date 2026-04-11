param(
  [string]$Version = "latest",
  [ValidateSet("package-manager", "direct")]
  [string]$Method = "direct",
  [string]$InstallDir = "$HOME\\.local\\bin"
)

$ErrorActionPreference = "Stop"

$Repo = "delinoio/oss"
$TagPrefix = "with-watch@v"
$WorkflowIdentityPattern = "^https://github.com/delinoio/oss/.github/workflows/release-with-watch.yml@"

function Resolve-LatestTag {
  $releases = Invoke-RestMethod -Uri "https://api.github.com/repos/$Repo/releases?per_page=200"
  $match = $releases | Where-Object { $_.tag_name -like "$TagPrefix*" } | Select-Object -First 1
  if (-not $match) {
    throw "[install.with-watch] failed to resolve latest tag"
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
    throw "[install.with-watch] checksum entry not found for $AssetName"
  }

  $expectedHash = ($expected -split "\s+")[0].ToLowerInvariant()
  $actualHash = (Get-FileHash -Path $FilePath -Algorithm SHA256).Hash.ToLowerInvariant()
  if ($expectedHash -ne $actualHash) {
    throw "[install.with-watch] checksum mismatch for $AssetName"
  }
}

function Download-Bundle {
  param(
    [string]$BaseUrl,
    [string]$AssetName,
    [string]$BundlePath
  )

  try {
    Invoke-WebRequest -Uri "$BaseUrl/$AssetName.sigstore.json" -OutFile $BundlePath
  }
  catch {
    throw "[install.with-watch] direct installs require releases published with Sigstore bundle sidecars"
  }
}

function Verify-Bundle {
  param(
    [string]$FilePath,
    [string]$BundlePath
  )

  if (-not (Get-Command cosign -ErrorAction SilentlyContinue)) {
    throw "[install.with-watch] cosign is required for direct installation"
  }

  cosign verify-blob `
    --bundle $BundlePath `
    --certificate-identity-regexp $WorkflowIdentityPattern `
    --certificate-oidc-issuer "https://token.actions.githubusercontent.com" `
    $FilePath | Out-Null
}

function Install-Direct {
  $tag = Resolve-Tag
  $baseUrl = "https://github.com/$Repo/releases/download/$tag"
  $architecture = [System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString().ToLowerInvariant()
  $assetArch = switch ($architecture) {
    "x64" { "amd64" }
    "arm64" { "arm64" }
    default { throw "[install.with-watch] unsupported Windows architecture: $architecture" }
  }
  $assetName = "with-watch-windows-$assetArch.zip"

  $tmpDir = Join-Path ([System.IO.Path]::GetTempPath()) ("with-watch-install-" + [System.Guid]::NewGuid().ToString("N"))
  New-Item -Path $tmpDir -ItemType Directory | Out-Null

  try {
    $assetPath = Join-Path $tmpDir $assetName
    $sumsPath = Join-Path $tmpDir "SHA256SUMS"
    $bundlePath = "$assetPath.sigstore.json"

    Write-Host "[install.with-watch] downloading $assetName"
    Invoke-WebRequest -Uri "$baseUrl/$assetName" -OutFile $assetPath
    Invoke-WebRequest -Uri "$baseUrl/SHA256SUMS" -OutFile $sumsPath
    Download-Bundle -BaseUrl $baseUrl -AssetName $assetName -BundlePath $bundlePath

    Verify-Checksum -FilePath $assetPath -Sha256SumsPath $sumsPath -AssetName $assetName
    Verify-Bundle -FilePath $assetPath -BundlePath $bundlePath

    $extractDir = Join-Path $tmpDir "extract"
    Expand-Archive -Path $assetPath -DestinationPath $extractDir -Force

    New-Item -Path $InstallDir -ItemType Directory -Force | Out-Null
    Copy-Item -Path (Join-Path $extractDir "with-watch.exe") -Destination (Join-Path $InstallDir "with-watch.exe") -Force

    Write-Host "[install.with-watch] installed with-watch.exe to $InstallDir"
  }
  finally {
    Remove-Item -Path $tmpDir -Recurse -Force -ErrorAction SilentlyContinue
  }
}

switch ($Method) {
  "package-manager" {
    # Compatibility shim: keep accepting the legacy package-manager flag until downstream
    # automation and docs stop sending it for Windows installs.
    Write-Warning "[install.with-watch] method=package-manager is deprecated on Windows and now maps to direct installation."
    Install-Direct
  }
  "direct" {
    Install-Direct
  }
}
