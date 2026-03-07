param(
  [string]$Version = "latest",
  [ValidateSet("package-manager", "direct")]
  [string]$Method = "package-manager",
  [string]$InstallDir = "$HOME\\.local\\bin"
)

$ErrorActionPreference = "Stop"

$Repo = "delinoio/oss"
$TagPrefix = "nodeup@v"
$WorkflowIdentityPattern = "^https://github.com/delinoio/oss/.github/workflows/release-nodeup.yml@"

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

function Verify-Signature {
  param(
    [string]$FilePath,
    [string]$SignaturePath,
    [string]$CertificatePath
  )

  if (-not (Get-Command cosign -ErrorAction SilentlyContinue)) {
    throw "[install.nodeup] cosign is required for direct installation"
  }

  cosign verify-blob `
    --certificate $CertificatePath `
    --signature $SignaturePath `
    --certificate-identity-regexp $WorkflowIdentityPattern `
    --certificate-oidc-issuer "https://token.actions.githubusercontent.com" `
    $FilePath | Out-Null
}

function Install-WithPackageManager {
  if (Get-Command winget -ErrorAction SilentlyContinue) {
    Write-Host "[install.nodeup] installing via winget"
    winget install --id DelinoIO.Nodeup --exact --accept-package-agreements --accept-source-agreements
    return $true
  }

  Write-Warning "[install.nodeup] winget is unavailable; falling back to direct installation"
  return $false
}

function Install-Direct {
  $tag = Resolve-Tag
  $baseUrl = "https://github.com/$Repo/releases/download/$tag"
  $assetName = "nodeup-windows-amd64.zip"

  $tmpDir = Join-Path ([System.IO.Path]::GetTempPath()) ("nodeup-install-" + [System.Guid]::NewGuid().ToString("N"))
  New-Item -Path $tmpDir -ItemType Directory | Out-Null

  try {
    $assetPath = Join-Path $tmpDir $assetName
    $sumsPath = Join-Path $tmpDir "SHA256SUMS"
    $signaturePath = "$assetPath.sig"
    $certificatePath = "$assetPath.pem"

    Write-Host "[install.nodeup] downloading $assetName"
    Invoke-WebRequest -Uri "$baseUrl/$assetName" -OutFile $assetPath
    Invoke-WebRequest -Uri "$baseUrl/SHA256SUMS" -OutFile $sumsPath
    Invoke-WebRequest -Uri "$baseUrl/$assetName.sig" -OutFile $signaturePath
    Invoke-WebRequest -Uri "$baseUrl/$assetName.pem" -OutFile $certificatePath

    Verify-Checksum -FilePath $assetPath -Sha256SumsPath $sumsPath -AssetName $assetName
    Verify-Signature -FilePath $assetPath -SignaturePath $signaturePath -CertificatePath $certificatePath

    $extractDir = Join-Path $tmpDir "extract"
    Expand-Archive -Path $assetPath -DestinationPath $extractDir -Force

    New-Item -Path $InstallDir -ItemType Directory -Force | Out-Null
    Copy-Item -Path (Join-Path $extractDir "nodeup.exe") -Destination (Join-Path $InstallDir "nodeup.exe") -Force

    Write-Host "[install.nodeup] installed nodeup.exe to $InstallDir"
  }
  finally {
    Remove-Item -Path $tmpDir -Recurse -Force -ErrorAction SilentlyContinue
  }
}

switch ($Method) {
  "package-manager" {
    if (-not (Install-WithPackageManager)) {
      Install-Direct
    }
  }
  "direct" {
    Install-Direct
  }
}
