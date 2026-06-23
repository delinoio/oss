param(
  [string]$Version = "latest",
  [ValidateSet("package-manager", "direct")]
  [string]$Method = "direct",
  [string]$InstallDir = "$HOME\\.local\\bin"
)

$ErrorActionPreference = "Stop"

$InstallerUrl = "https://raw.githubusercontent.com/delinoio/oss/refs/heads/main/scripts/install/nodeup.ps1"
$Installer = Join-Path ([System.IO.Path]::GetTempPath()) ("nodeup-install-" + [System.Guid]::NewGuid().ToString("N") + ".ps1")

try {
  Invoke-WebRequest -Uri $InstallerUrl -OutFile $Installer -UseBasicParsing
  Unblock-File -LiteralPath $Installer -ErrorAction SilentlyContinue

  $PowerShell = (Get-Process -Id $PID).Path
  & $PowerShell -NoProfile -ExecutionPolicy Bypass -File $Installer -Version $Version -Method $Method -InstallDir $InstallDir
  if ($LASTEXITCODE -ne 0) {
    exit $LASTEXITCODE
  }
}
finally {
  Remove-Item -LiteralPath $Installer -Force -ErrorAction SilentlyContinue
}
