$ErrorActionPreference = "Stop"

if ($args.Count -ne 1) { throw "DevHUD signing expects exactly one input path" }
$inputPath = $args[0]
foreach ($name in @("DEVHUD_WINDOWS_SIGNING_PFX_PATH", "DEVHUD_WINDOWS_SIGNING_PFX_PASSWORD", "DEVHUD_WINDOWS_CERTIFICATE_SHA256", "DEVHUD_WINDOWS_TIMESTAMP_URL")) {
  if ([string]::IsNullOrWhiteSpace([Environment]::GetEnvironmentVariable($name))) { throw "Missing required Windows signing input: $name" }
}
if (-not (Test-Path -LiteralPath $inputPath -PathType Leaf)) { throw "Windows signing input does not exist" }
if (-not (Test-Path -LiteralPath $env:DEVHUD_WINDOWS_SIGNING_PFX_PATH -PathType Leaf)) { throw "Windows signing PFX does not exist" }
if ($env:DEVHUD_WINDOWS_TIMESTAMP_URL -notmatch '^https://') { throw "Windows timestamp URL must use HTTPS" }

& signtool.exe sign /fd SHA256 /td SHA256 /tr $env:DEVHUD_WINDOWS_TIMESTAMP_URL /f $env:DEVHUD_WINDOWS_SIGNING_PFX_PATH /p $env:DEVHUD_WINDOWS_SIGNING_PFX_PASSWORD $inputPath
if ($LASTEXITCODE -ne 0) { throw "signtool sign failed" }
& signtool.exe verify /pa /all /v $inputPath
if ($LASTEXITCODE -ne 0) { throw "signtool verification failed" }
$signature = Get-AuthenticodeSignature -LiteralPath $inputPath
$sha256 = [System.Security.Cryptography.SHA256]::Create()
try { $thumbprint = ([BitConverter]::ToString($sha256.ComputeHash($signature.SignerCertificate.RawData))).Replace("-", "").ToLowerInvariant() } finally { $sha256.Dispose() }
$expected = $env:DEVHUD_WINDOWS_CERTIFICATE_SHA256.Replace(" ", "").ToLowerInvariant()
if ($signature.Status -ne "Valid" -or $thumbprint -ne $expected) { throw "Windows signer certificate did not match the pinned SHA-256 thumbprint" }
