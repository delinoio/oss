$ErrorActionPreference = 'Stop'
# Disposable Windows CI only. Register the repository's OFL fixture as a system
# font so DirectWrite fallback is tested without optional language-pack downloads.
$fontSource = Join-Path $PSScriptRoot '../../../crates/forge-tree-doc/assets/fonts/noto-sans-kr/NotoSansKR-VF.ttf'
$fontName = 'ReactForge-NotoSansKR-VF.ttf'
$fontDestination = Join-Path $env:WINDIR "Fonts/$fontName"
Copy-Item -LiteralPath $fontSource -Destination $fontDestination -Force
New-ItemProperty -Path 'HKLM:\SOFTWARE\Microsoft\Windows NT\CurrentVersion\Fonts' -Name 'Noto Sans KR (TrueType)' -Value $fontName -PropertyType String -Force | Out-Null
Add-Type -TypeDefinition @'
using System.Runtime.InteropServices;
public static class ForgeTestFonts {
    [DllImport("gdi32.dll", CharSet = CharSet.Unicode)]
    public static extern int AddFontResourceW(string path);
}
'@
if ([ForgeTestFonts]::AddFontResourceW($fontDestination) -eq 0) { throw 'System test font registration failed.' }
Write-Output 'Registered OFL CJK test font; Windows supplies Segoe UI and Segoe UI Emoji.'
