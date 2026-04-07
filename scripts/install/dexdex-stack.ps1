param(
  [string]$Version = "latest",
  [ValidateSet("package-manager", "direct")]
  [string]$Method = "direct",
  [string]$InstallDir = "$HOME\\.local\\bin"
)

$ErrorActionPreference = "Stop"

$Repo = "delinoio/oss"
$TagPrefix = "dexdex@v"
$WorkflowIdentityPattern = "^https://github.com/delinoio/oss/.github/workflows/release-dexdex.yml@"

function Resolve-LatestTag {
  $releases = Invoke-RestMethod -Uri "https://api.github.com/repos/$Repo/releases?per_page=200"
  $match = $releases | Where-Object { $_.tag_name -like "$TagPrefix*" } | Select-Object -First 1
  if (-not $match) {
    throw "[install.dexdex] failed to resolve latest tag"
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
    throw "[install.dexdex] checksum entry not found for $AssetName"
  }

  $expectedHash = ($expected -split "\s+")[0].ToLowerInvariant()
  $actualHash = (Get-FileHash -Path $FilePath -Algorithm SHA256).Hash.ToLowerInvariant()
  if ($expectedHash -ne $actualHash) {
    throw "[install.dexdex] checksum mismatch for $AssetName"
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
    throw "[install.dexdex] direct installs require releases published with Sigstore bundle sidecars"
  }
}

function Verify-Bundle {
  param(
    [string]$FilePath,
    [string]$BundlePath
  )

  if (-not (Get-Command cosign -ErrorAction SilentlyContinue)) {
    throw "[install.dexdex] cosign is required for direct installation"
  }

  cosign verify-blob `
    --bundle $BundlePath `
    --certificate-identity-regexp $WorkflowIdentityPattern `
    --certificate-oidc-issuer "https://token.actions.githubusercontent.com" `
    $FilePath | Out-Null
}

function Download-AndVerify {
  param(
    [string]$BaseUrl,
    [string]$AssetName,
    [string]$TempDir,
    [string]$Sha256SumsPath
  )

  $assetPath = Join-Path $TempDir $AssetName
  $bundlePath = "$assetPath.sigstore.json"

  Invoke-WebRequest -Uri "$BaseUrl/$AssetName" -OutFile $assetPath
  Download-Bundle -BaseUrl $BaseUrl -AssetName $AssetName -BundlePath $bundlePath

  Verify-Checksum -FilePath $assetPath -Sha256SumsPath $Sha256SumsPath -AssetName $AssetName
  Verify-Bundle -FilePath $assetPath -BundlePath $bundlePath

  return $assetPath
}

function Install-Direct {
  $tag = Resolve-Tag
  $baseUrl = "https://github.com/$Repo/releases/download/$tag"

  $tmpDir = Join-Path ([System.IO.Path]::GetTempPath()) ("dexdex-install-" + [System.Guid]::NewGuid().ToString("N"))
  New-Item -Path $tmpDir -ItemType Directory | Out-Null

  try {
    $sumsPath = Join-Path $tmpDir "SHA256SUMS"
    Invoke-WebRequest -Uri "$baseUrl/SHA256SUMS" -OutFile $sumsPath

    $mainAsset = "dexdex-main-server-windows-amd64.zip"
    $workerAsset = "dexdex-worker-server-windows-amd64.zip"
    $desktopAsset = "dexdex-desktop-windows-amd64.msi"

    $mainPath = Download-AndVerify -BaseUrl $baseUrl -AssetName $mainAsset -TempDir $tmpDir -Sha256SumsPath $sumsPath
    $workerPath = Download-AndVerify -BaseUrl $baseUrl -AssetName $workerAsset -TempDir $tmpDir -Sha256SumsPath $sumsPath
    $desktopPath = Download-AndVerify -BaseUrl $baseUrl -AssetName $desktopAsset -TempDir $tmpDir -Sha256SumsPath $sumsPath

    $extractMain = Join-Path $tmpDir "extract-main"
    $extractWorker = Join-Path $tmpDir "extract-worker"

    Expand-Archive -Path $mainPath -DestinationPath $extractMain -Force
    Expand-Archive -Path $workerPath -DestinationPath $extractWorker -Force

    New-Item -Path $InstallDir -ItemType Directory -Force | Out-Null
    Copy-Item -Path (Join-Path $extractMain "dexdex-main-server.exe") -Destination (Join-Path $InstallDir "dexdex-main-server.exe") -Force
    Copy-Item -Path (Join-Path $extractWorker "dexdex-worker-server.exe") -Destination (Join-Path $InstallDir "dexdex-worker-server.exe") -Force

    Write-Host "[install.dexdex] installed server executables to $InstallDir"
    Write-Host "[install.dexdex] installing desktop MSI"
    Start-Process -FilePath "msiexec.exe" -ArgumentList "/i `"$desktopPath`" /qn" -Wait
  }
  finally {
    Remove-Item -Path $tmpDir -Recurse -Force -ErrorAction SilentlyContinue
  }
}

switch ($Method) {
  "package-manager" {
    # Compatibility shim: keep accepting the legacy package-manager flag until downstream
    # automation and docs stop sending it for Windows installs.
    Write-Warning "[install.dexdex] method=package-manager is deprecated on Windows and now maps to direct installation."
    Install-Direct
  }
  "direct" {
    Install-Direct
  }
}
