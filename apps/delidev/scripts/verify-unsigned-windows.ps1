param(
    [Parameter(Mandatory = $true)][string]$Directory,
    [Parameter(Mandatory = $true)][string]$Installer
)
$ErrorActionPreference = 'Stop'
# No certificate import, timestamp request or signing occurs in a dry run.
# Validate that no ambient configuration signed the product unexpectedly.
foreach ($path in @((Join-Path $Directory 'delidev-desktop.exe'), (Join-Path $Directory 'delidev.exe'), $Installer)) {
    $signature = Get-AuthenticodeSignature -LiteralPath $path
    if ($signature.Status -ne 'NotSigned') {
        throw 'The keyless dry-run product must be explicitly unsigned.'
    }
}
Write-Output 'DeliDev Windows product and installer are unsigned as requested.'
