param(
    [string]$Directory,
    [Parameter(Mandatory = $true)][string]$Installer,
    [string]$Desktop,
    [string]$Worker
)
$ErrorActionPreference = 'Stop'
# No certificate import, timestamp request or signing occurs in a dry run.
# Validate that no ambient configuration signed the product unexpectedly.
if ($Directory -and -not $Desktop -and -not $Worker) {
    $paths = @((Join-Path $Directory 'delidev-desktop.exe'), (Join-Path $Directory 'delidev.exe'), $Installer)
} elseif (-not $Directory -and $Desktop -and $Worker) {
    $paths = @($Desktop, $Worker, $Installer)
} else {
    throw 'Select either the extracted bundle or both original update product paths.'
}
foreach ($path in $paths) {
    $signature = Get-AuthenticodeSignature -LiteralPath $path
    if ($signature.Status -ne 'NotSigned') {
        throw 'The keyless dry-run product must be explicitly unsigned.'
    }
}
Write-Output 'DeliDev Windows product and installer are unsigned as requested.'
