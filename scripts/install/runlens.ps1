[CmdletBinding()]
param(
  [Parameter(Mandatory=$true)][ValidatePattern('^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$')][string]$Version,
  [string]$InstallDir = (Join-Path $HOME '.local/bin'),
  [string]$ArchiveDir = ''
)
$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
if ([Environment]::OSVersion.Platform -ne 'Win32NT') { throw 'Windows is required.' }
$arch = switch ([System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString()) {
  'X64' { 'amd64' }
  'Arm64' { 'arm64' }
  default { throw 'Unsupported architecture.' }
}
$null = Get-Command cosign -ErrorAction Stop
$asset = "runlens-windows-$arch.zip"
$tag = "runlens@v$Version"
$base = "https://github.com/delinoio/oss/releases/download/$tag"
$identity = "https://github.com/delinoio/oss/.github/workflows/release-runlens.yml@refs/tags/$tag"
$work = Join-Path ([IO.Path]::GetTempPath()) ([Guid]::NewGuid().ToString('N'))
$staged = $null
$zip = $null
try {
  $null = New-Item -ItemType Directory -Path $work
  # Restrict temporary downloads to this user before any release data is written.
  $acl = Get-Acl -LiteralPath $work
  $acl.SetAccessRuleProtection($true, $false)
  $user = [Security.Principal.WindowsIdentity]::GetCurrent().User
  $rule = [Security.AccessControl.FileSystemAccessRule]::new($user, 'FullControl', 'ContainerInherit,ObjectInherit', 'None', 'Allow')
  $acl.AddAccessRule($rule)
  Set-Acl -LiteralPath $work -AclObject $acl
  foreach ($name in @($asset, "$asset.sigstore.json", 'SHA256SUMS', 'SHA256SUMS.sigstore.json')) {
    $destination = Join-Path $work $name
    if ($ArchiveDir) { Copy-Item -LiteralPath (Join-Path $ArchiveDir $name) -Destination $destination }
    else {
      # HttpClient permits enforcing HTTPS on every redirect, including asset storage redirects.
      $handler = [Net.Http.HttpClientHandler]::new()
      $handler.AllowAutoRedirect = $false
      $client = [Net.Http.HttpClient]::new($handler)
      try {
        $uri = [Uri]"$base/$name"
        for ($redirect = 0; $redirect -le 8; $redirect++) {
          if ($uri.Scheme -ne 'https') { throw 'HTTPS is required.' }
          $response = $client.GetAsync($uri, [Net.Http.HttpCompletionOption]::ResponseHeadersRead).GetAwaiter().GetResult()
          try {
            if ([int]$response.StatusCode -ge 300 -and [int]$response.StatusCode -lt 400) {
              $uri = [Uri]::new($uri, $response.Headers.Location)
              continue
            }
            $null = $response.EnsureSuccessStatusCode()
            $stream = [IO.File]::Create($destination)
            try { $response.Content.CopyToAsync($stream).GetAwaiter().GetResult() } finally { $stream.Dispose() }
            break
          } finally { $response.Dispose() }
        }
        if (-not (Test-Path -LiteralPath $destination)) { throw 'Too many redirects.' }
      } finally { $client.Dispose(); $handler.Dispose() }
    }
  }
  foreach ($name in @('SHA256SUMS', $asset)) {
    & cosign verify-blob --bundle (Join-Path $work "$name.sigstore.json") --certificate-identity $identity --certificate-oidc-issuer 'https://token.actions.githubusercontent.com' (Join-Path $work $name)
    if ($LASTEXITCODE -ne 0) { throw 'Release authentication failed.' }
  }
  $matches = @(Get-Content -LiteralPath (Join-Path $work 'SHA256SUMS') | Where-Object { $_ -cmatch ('^[0-9a-f]{64}  ' + [Regex]::Escape($asset) + '$') })
  if ($matches.Count -ne 1) { throw 'Missing or duplicate checksum.' }
  $actual = (Get-FileHash -Algorithm SHA256 -LiteralPath (Join-Path $work $asset)).Hash.ToLowerInvariant()
  if ($actual -cne $matches[0].Substring(0, 64)) { throw 'Archive checksum mismatch.' }
  Add-Type -AssemblyName System.IO.Compression.FileSystem
  $zip = [IO.Compression.ZipFile]::OpenRead((Join-Path $work $asset))
  if ($zip.Entries.Count -ne 2 -or (@($zip.Entries.FullName | Sort-Object) -join ',') -cne 'LICENSES.txt,runlens.exe') { throw 'Unexpected archive members.' }
  $null = New-Item -ItemType Directory -Force -Path $InstallDir
  $destination = Join-Path $InstallDir 'runlens.exe'
  if ((Test-Path -LiteralPath $destination) -and ((Get-Item -LiteralPath $destination).Attributes -band [IO.FileAttributes]::ReparsePoint)) { throw 'Refusing to replace a link.' }
  $staged = Join-Path $InstallDir ('.runlens-' + [Guid]::NewGuid().ToString('N') + '.exe')
  [IO.Compression.ZipFileExtensions]::ExtractToFile($zip.GetEntry('runlens.exe'), $staged, $false)
  $output = & $staged --version
  if ($LASTEXITCODE -ne 0 -or $output -cne "runlens $Version") { throw 'Executable version mismatch.' }
  if (Test-Path -LiteralPath $destination) { [IO.File]::Replace($staged, $destination, $null) }
  else { [IO.File]::Move($staged, $destination) }
  $staged = $null
  Write-Host "Installed Runlens $Version. Run runlens doctor to check host tracing support."
} finally {
  if ($null -ne $zip) { $zip.Dispose() }
  if ($null -ne $staged -and (Test-Path -LiteralPath $staged)) { Remove-Item -LiteralPath $staged -Force }
  if (Test-Path -LiteralPath $work) { Remove-Item -LiteralPath $work -Recurse -Force }
}
