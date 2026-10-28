// SPDX-License-Identifier: Apache-2.0
package main

// This adapter follows the assessed package/activation contract at upstream
// 88aaa754566d119b6b8fc898f8fb47602387d82e. The ancestry check is a metadata
// snapshot, not a handle-bound filesystem attestation. It rejects reparse points
// and compares fresh snapshots before and after activation. Do not broaden the
// package/version gate without a new native acceptance assessment.
const nativeCodexPackageScript = `$ErrorActionPreference='Stop'
$inputData=[Console]::In.ReadToEnd()|ConvertFrom-Json
function Path-Stamp([string]$path) {
 if($path.Length -gt 8192 -or $path -notmatch '^[A-Za-z]:\\' -or [IO.Path]::GetFullPath($path) -ine $path){throw 'Noncanonical path'}
 $current=[IO.Path]::GetPathRoot($path)
 $parts=@($path.Substring($current.Length).Split('\')|Where-Object {$_ -ne ''})
 if($parts.Count -gt 128){throw 'Path depth'}
 $stamps=@()
 for($i=-1;$i -lt $parts.Count;$i++) {
  if($i -ge 0){$current=Join-Path $current $parts[$i]}
  $item=Get-Item -LiteralPath $current -Force
  if(($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -or ($i -lt $parts.Count-1 -and -not $item.PSIsContainer)){throw 'Unsafe ancestry'}
  $stamps+=($item.FullName.ToLowerInvariant()+':'+$item.CreationTimeUtc.Ticks)
 }
 if($item.PSIsContainer){throw 'Expected regular file'}
 return (($stamps -join '|')+':'+$item.Length+':'+$item.LastWriteTimeUtc.Ticks)
}
function Package-Proof {
 $packages=@(Get-AppxPackage -Name 'OpenAI.Codex')
 if($packages.Count -ne 1){throw 'Package not unique'}
 $p=$packages[0]
 if($p.PackageFamilyName -cne 'OpenAI.Codex_2p2nqsd0c76g0' -or $p.PublisherId -cne '2p2nqsd0c76g0' -or [string]$p.Version -cne '26.924.2738.0' -or $p.PackageFullName -cnotmatch '^OpenAI\.Codex_26\.924\.2738\.0_(x64|arm64|x86|neutral)_[A-Za-z0-9.-]*_2p2nqsd0c76g0$'){throw 'Unassessed package'}
 if([IO.Path]::GetFileName($p.InstallLocation) -cne $p.PackageFullName -or -not $p.Publisher -or $p.Publisher.Length -gt 1024){throw 'Package identity'}
 $exe=Join-Path $p.InstallLocation 'app\ChatGPT.exe'
 $manifestPath=Join-Path $p.InstallLocation 'AppxManifest.xml'
 $exeStamp=Path-Stamp $exe; $manifestStamp=Path-Stamp $manifestPath
 $manifest=Get-AppxPackageManifest -Package $p.PackageFullName
 $identity=$manifest.Package.Identity
 if($identity.Name -cne 'OpenAI.Codex' -or $identity.Publisher -cne $p.Publisher -or $identity.Version -cne '26.924.2738.0'){throw 'Manifest identity'}
 $apps=@($manifest.Package.Applications.Application|Where-Object {$_.Id -ceq 'App'})
 if($apps.Count -ne 1 -or $apps[0].Executable.Replace('/','\') -ine 'app\ChatGPT.exe'){throw 'Manifest application'}
 $xml=[Text.Encoding]::UTF8.GetBytes($manifest.OuterXml)
 if($xml.Length -gt 1048576){throw 'Manifest size'}
 $sha=[Security.Cryptography.SHA256]::Create()
 try{$digest=[Convert]::ToBase64String($sha.ComputeHash($xml))}finally{$sha.Dispose()}
 if((Path-Stamp $exe) -cne $exeStamp -or (Path-Stamp $manifestPath) -cne $manifestStamp){throw 'Package changed'}
 return [pscustomobject]@{Full=$p.PackageFullName;Family=$p.PackageFamilyName;Root=$p.InstallLocation;Publisher=$p.Publisher;Exe=$exe;Stamp=$exeStamp;Manifest=$manifestStamp;Digest=$digest}
}
Import-Module Appx -ErrorAction Stop
$proof=Package-Proof
if($inputData.operation -eq 'launch' -and $inputData.root) {
 if($proof.Root -cne $inputData.root -or $proof.Stamp -cne $inputData.stamp -or $proof.Manifest -cne $inputData.manifest -or $proof.Digest -cne $inputData.digest -or $proof.Full -cne $inputData.full){throw 'Approved installation changed'}
}
$pidValue=0
$arguments=''
if($inputData.operation -eq 'launch') {
 # Refuse every existing ChatGPT process, even when its executable cannot be read.
 # Never terminate or attach to a process that this invocation did not create.
 if(@(Get-CimInstance Win32_Process -Filter "Name='ChatGPT.exe'").Count -ne 0){throw 'Application already running'}
 $pac=[string]$inputData.pac
 if($pac -cnotmatch '^http://127\.0\.0\.1:[0-9]+/[a-zA-Z0-9-]{16,80}/proxy\.pac$'){throw 'Invalid PAC'}
 Add-Type -TypeDefinition @'
using System;
using System.Runtime.InteropServices;
using System.Text;
public static class TaiRouterCodexActivation {
 [ComImport, Guid("2E941141-7F97-4756-BA1D-9DECDE894A3D"), InterfaceType(ComInterfaceType.InterfaceIsIUnknown)]
 interface Activation {
  [PreserveSig] int ActivateApplication([MarshalAs(UnmanagedType.LPWStr)] string id, [MarshalAs(UnmanagedType.LPWStr)] string args, uint flags, out uint pid);
  [PreserveSig] int ActivateForFile([MarshalAs(UnmanagedType.LPWStr)] string id, IntPtr files, [MarshalAs(UnmanagedType.LPWStr)] string verb, out uint pid);
  [PreserveSig] int ActivateForProtocol([MarshalAs(UnmanagedType.LPWStr)] string id, IntPtr items, out uint pid);
 }
 [DllImport("ole32.dll")] static extern int CoCreateInstance(ref Guid clsid, IntPtr outer, uint context, ref Guid iid, [MarshalAs(UnmanagedType.Interface)] out Activation instance);
 [DllImport("kernel32.dll", SetLastError=true)] static extern IntPtr OpenProcess(uint rights, bool inherit, uint pid);
 [DllImport("kernel32.dll")] static extern bool CloseHandle(IntPtr handle);
 [DllImport("kernel32.dll", CharSet=CharSet.Unicode)] static extern int GetPackageFullName(IntPtr handle, ref uint length, StringBuilder name);
 [DllImport("shell32.dll", CharSet=CharSet.Unicode, SetLastError=true)] static extern IntPtr CommandLineToArgvW(string command, out int count);
 [DllImport("kernel32.dll")] static extern IntPtr LocalFree(IntPtr memory);
 public static uint Start(string id, string args) {
  Guid clsid=new Guid("45BA127D-10A8-46EA-8AB7-56EA9078943C"), iid=new Guid("2E941141-7F97-4756-BA1D-9DECDE894A3D");
  Activation manager;
  Marshal.ThrowExceptionForHR(CoCreateInstance(ref clsid, IntPtr.Zero, 4, ref iid, out manager));
  try {uint pid; Marshal.ThrowExceptionForHR(manager.ActivateApplication(id,args,0,out pid)); return pid;}
  finally {Marshal.FinalReleaseComObject(manager);}
 }
 public static string Package(uint pid) {
  IntPtr handle=OpenProcess(0x1000,false,pid);
  if(handle==IntPtr.Zero)throw new InvalidOperationException("Process unavailable");
  try {
   uint length=0; if(GetPackageFullName(handle,ref length,null)!=122)throw new InvalidOperationException("Package unavailable");
   StringBuilder name=new StringBuilder((int)length);
   if(GetPackageFullName(handle,ref length,name)!=0)throw new InvalidOperationException("Package changed");
   return name.ToString();
  } finally {CloseHandle(handle);}
 }
 public static bool Arguments(string command, string expected) {
  int count; IntPtr values=CommandLineToArgvW(command,out count);
  if(values==IntPtr.Zero)return false;
  try {
   int matches=0;
   for(int i=1;i<count;i++) {
    string value=Marshal.PtrToStringUni(Marshal.ReadIntPtr(values,i*IntPtr.Size));
    if(value==expected)matches++;
    else if(value.StartsWith("--proxy-") || value=="--no-proxy-server")return false;
   }
   return matches==1;
  } finally {LocalFree(values);}
 }
}
'@
 # Repeat the read after compiling the activation adapter; compilation can be slow.
 if(($proof|ConvertTo-Json -Compress) -cne ((Package-Proof)|ConvertTo-Json -Compress)){throw 'Package changed before activation'}
 if(@(Get-CimInstance Win32_Process -Filter "Name='ChatGPT.exe'").Count -ne 0){throw 'Application already running'}
 $arguments='--proxy-pac-url='+$pac
 $pidValue=[TaiRouterCodexActivation]::Start($proof.Family+'!App',$arguments)
 if($pidValue -eq 0 -or [TaiRouterCodexActivation]::Package($pidValue) -cne $proof.Full){throw 'Activated package mismatch'}
 $process=Get-CimInstance Win32_Process -Filter ('ProcessId='+$pidValue)
 if(-not $process -or $process.ExecutablePath -ine $proof.Exe -or -not [TaiRouterCodexActivation]::Arguments($process.CommandLine,$arguments)){throw 'Activated executable or arguments mismatch'}
} elseif($inputData.operation -ne 'observe') {throw 'Unknown operation'}
if(($proof|ConvertTo-Json -Compress) -cne ((Package-Proof)|ConvertTo-Json -Compress)){throw 'Package changed after operation'}
[pscustomobject]@{root=$proof.Root;executableStamp=$proof.Stamp;manifestStamp=$proof.Manifest;manifestDigest=$proof.Digest;verified=$true;packageFullName=$proof.Full;family=$proof.Family;version='26.924.2738.0';executable='app\ChatGPT.exe';pid=$pidValue;arguments=$arguments}|ConvertTo-Json -Compress
`
