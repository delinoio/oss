param(
  [Parameter(Mandatory)][ValidateSet("x86_64-pc-windows-msvc", "aarch64-pc-windows-msvc")][string]$Target,
  [Parameter(Mandatory)][ValidateSet("win32-x64-msvc", "win32-arm64-msvc")][string]$Suffix
)

$ErrorActionPreference = "Stop"
Set-StrictMode -Version Latest
if (($Target -eq "x86_64-pc-windows-msvc") -ne ($Suffix -eq "win32-x64-msvc")) {
  throw "[pnport.windows] target and suffix disagree"
}
$expected = if ($Suffix -eq "win32-x64-msvc") { "X64" } else { "Arm64" }
if ([System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString() -cne $expected) {
  throw "[pnport.windows] native host architecture required"
}
function Invoke-Checked([string]$Program, [string[]]$Arguments) {
  & $Program @Arguments
  if ($LASTEXITCODE -ne 0) {
    if ($Program -eq "cargo" -and $Arguments -contains "windows") {
      Write-Output "[pnport.windows] diagnostic Node replay; the original validation remains failed"
      $env:PNPORT_WINDOWS_DEBUGGER = "1"
      try { & $Program @Arguments "node_filesystem_crypto_loopback_and_replacement_environment" "--" "--exact" "--nocapture" }
      finally { Remove-Item Env:PNPORT_WINDOWS_DEBUGGER -ErrorAction SilentlyContinue }
    }
    throw "[pnport.windows] validation command failed: $Program"
  }
}
Write-Output (@{ event = "pnport_windows_development_validation"; target = $Target; revision = (& git rev-parse HEAD).Trim(); acceptance = "development-only" } | ConvertTo-Json -Compress)
Invoke-Checked "cargo" @("build", "--locked", "-p", "pnport", "-p", "pnport-preload", "--target", $Target)
Invoke-Checked "cargo" @("test", "--locked", "-p", "pnport-core", "--lib", "--target", $Target)
Invoke-Checked "cargo" @("test", "--locked", "-p", "pnport", "--bin", "pnport", "--target", $Target)
Invoke-Checked "cargo" @("test", "--locked", "-p", "pnport", "--test", "windows", "--target", $Target)
Invoke-Checked "cargo" @("build", "--locked", "--release", "-p", "pnport", "-p", "pnport-preload", "--target", $Target)
$binary = Join-Path $PWD "target/$Target/release/pnport.exe"
$preload = Join-Path $PWD "target/$Target/release/pnport_preload.dll"
Invoke-Checked "node" @("packages/pnport/scripts/package.mjs", "development", "--target", $Target, "--binary", $binary, "--preload", $preload)
$installed = Join-Path $env:RUNNER_TEMP "pnport-installed-$Suffix"
if (Test-Path -LiteralPath $installed) { throw "[pnport.windows] installed fixture must be fresh" }
New-Item -ItemType Directory -Path $installed | Out-Null
$archive = Join-Path $PWD "packages/pnport/dist/development/pnport-$Suffix.tar.gz"
Invoke-Checked "tar.exe" @("-xzf", $archive, "-C", $installed)
foreach ($name in @("pnport.exe", "pnport_preload.dll", "LICENSE", "LICENSE.fspy", "LICENSE.detours")) {
  if (-not (Test-Path -LiteralPath (Join-Path $installed $name) -PathType Leaf)) { throw "[pnport.windows] incomplete development archive" }
}
foreach ($pair in @(@("pnport.exe", $binary), @("pnport_preload.dll", $preload))) {
  if ((Get-FileHash -LiteralPath (Join-Path $installed $pair[0]) -Algorithm SHA256).Hash -cne (Get-FileHash -LiteralPath $pair[1] -Algorithm SHA256).Hash) { throw "[pnport.windows] installed payload mismatch" }
}
$env:PNPORT_TEST_BINARY = Join-Path $installed "pnport.exe"
try {
  Invoke-Checked $env:PNPORT_TEST_BINARY @("--version")
  Invoke-Checked "cargo" @("test", "--locked", "-p", "pnport", "--test", "windows", "--target", $Target)
} finally {
  Remove-Item Env:PNPORT_TEST_BINARY -ErrorAction SilentlyContinue
}
# The archive and native fixtures do not authorize npm/PowerShell publication,
# certify Windows 10, or replace the full installed-consumer/benchmark gates.
Write-Output (@{ event = "pnport_windows_development_validation_complete"; target = $Target; acceptance = "development-only" } | ConvertTo-Json -Compress)
