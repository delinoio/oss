// SPDX-License-Identifier: Apache-2.0
import { createHash, randomBytes } from 'node:crypto';
import { execFileSync, spawnSync } from 'node:child_process';
import { copyFileSync, lstatSync, mkdirSync, mkdtempSync, readFileSync, readdirSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { dryRunEnvironment } from '../../apps/delidev/scripts/bundle-macos-dry-run.mjs';
import { requireValue } from './delidev-release.mjs';

export const macSecrets = ['DELIDEV_MACOS_CERTIFICATE_BASE64', 'DELIDEV_MACOS_CERTIFICATE_PASSWORD', 'DELIDEV_MACOS_APP_PROFILE_BASE64', 'DELIDEV_MACOS_WIDGET_PROFILE_BASE64', 'DELIDEV_MACOS_SELECTION_PROFILE_BASE64', 'DELIDEV_APPLE_NOTARY_KEY_BASE64', 'DELIDEV_APPLE_NOTARY_KEY_ID', 'DELIDEV_APPLE_NOTARY_ISSUER_ID'];
export function validateSigningConfiguration(env) {
  requireValue(macSecrets.every(n => typeof env[n] === 'string' && env[n].length > 0), 'DeliDev macOS signing configuration is incomplete');
  requireValue(/^[A-Z0-9]{10}$/.test(env.DELIDEV_APPLE_TEAM_ID ?? '') && /^[A-Fa-f0-9]{40}$/.test(env.DELIDEV_MACOS_CERTIFICATE_SHA1 ?? ''), 'DeliDev macOS signing identity is invalid');
  requireValue(/^[A-Z0-9]{10}$/.test(env.DELIDEV_APPLE_NOTARY_KEY_ID) && /^[a-f0-9-]{36}$/i.test(env.DELIDEV_APPLE_NOTARY_ISSUER_ID), 'DeliDev notarization identity is invalid');
}
export function validateProfile(profile, id, team, certificate, now = Date.now()) {
  const e = profile.Entitlements;
  requireValue(profile.TeamIdentifier?.length === 1 && profile.TeamIdentifier[0] === team && Date.parse(profile.ExpirationDate) > now && Array.isArray(profile.DeveloperCertificates) && profile.DeveloperCertificates.some(c => createHash('sha1').update(Buffer.from(c,'base64')).digest('hex').toUpperCase() === certificate.toUpperCase()), 'DeliDev provisioning profile authority mismatch');
  requireValue(e && e['com.apple.application-identifier'] === `${team}.${id}` && e['com.apple.developer.team-identifier'] === team && e['get-task-allow'] !== true && e['com.apple.security.get-task-allow'] !== true && JSON.stringify(e['com.apple.security.application-groups']) === JSON.stringify(['group.io.delino.delidev']), 'DeliDev provisioning profile capabilities mismatch');
  requireValue(profile.ProvisionsAllDevices === true, 'DeliDev requires a Developer ID provisioning profile');
  return e;
}
function decoded(value) {
  requireValue(value.length <= 4 * 1024 ** 2 && /^[A-Za-z0-9+/]+={0,2}$/.test(value), 'Invalid signing material encoding');
  const bytes = Buffer.from(value,'base64');
  requireValue(bytes.toString('base64') === value, 'Noncanonical signing material encoding');
  return bytes;
}
export function signMacOS({ bundle, worker, desktop, version, source = process.env, runCommand }) {
  validateSigningConfiguration(source);
  const env = dryRunEnvironment(source);
  const directory = mkdtempSync(join(tmpdir(),'delidev-sign-'));
  const keychain = join(directory,'release.keychain-db');
  const certificate = source.DELIDEV_MACOS_CERTIFICATE_SHA1.toUpperCase();
  const run = (program,args,input) => {
    if (runCommand) return runCommand(program,args,input,env);
    try {
      const result = spawnSync(program,args,{ env, input, encoding:'utf8', timeout: 20*60*1000, maxBuffer: 8*1024**2 });
      if (result.error || result.status !== 0) throw new Error('Signing child failed');
      return program === '/usr/bin/codesign' && args.includes('--display') ? result.stdout + result.stderr : result.stdout;
    }
    catch { throw new Error(`DeliDev macOS signing command failed: ${program}`); }
  };
  const material = (name, value) => {
    const file = join(directory,name), bytes = decoded(value);
    try { writeFileSync(file,bytes,{flag:'wx',mode:0o600}); } finally { bytes.fill(0); }
    return file;
  };
  // Do not change the user's/default keychain search list. This temporary
  // keychain is passed explicitly to codesign and removed in all outcomes.
  const password = randomBytes(32).toString('hex');
  let created = false;
  try {
    run('/usr/bin/security',['create-keychain','-p',password,keychain]); created = true;
    run('/usr/bin/security',['set-keychain-settings','-lut','21600',keychain]);
    run('/usr/bin/security',['unlock-keychain','-p',password,keychain]);
    const p12 = material('identity.p12',source.DELIDEV_MACOS_CERTIFICATE_BASE64);
    run('/usr/bin/security',['import',p12,'-k',keychain,'-P',source.DELIDEV_MACOS_CERTIFICATE_PASSWORD,'-T','/usr/bin/codesign']);
    run('/usr/bin/security',['set-key-partition-list','-S','apple-tool:,apple:,codesign:','-s','-k',password,keychain]);
    const identities = run('/usr/bin/security',['find-identity','-v','-p','codesigning',keychain]);
    requireValue(identities.includes(certificate) && identities.includes('Developer ID Application:'), 'Imported Developer ID identity mismatch');
    const profiles = [
      [bundle,'io.delino.delidev',source.DELIDEV_MACOS_APP_PROFILE_BASE64],
      [join(bundle,'Contents/PlugIns/DeliDevWidget.appex'),'io.delino.delidev.widget',source.DELIDEV_MACOS_WIDGET_PROFILE_BASE64],
      [join(bundle,'Contents/PlugIns/DeliDevWidgetSelection.appex'),'io.delino.delidev.widget.selection',source.DELIDEV_MACOS_SELECTION_PROFILE_BASE64],
    ];
    const entitlements = new Map();
    for (const [path,id,value] of profiles) {
      const profileFile = material(`${id}.provisionprofile`,value);
      const xml = run('/usr/bin/security',['cms','-D','-i',profileFile]);
      // plistlib supports binary data and dates, unlike plutil's JSON converter.
      const profile = JSON.parse(run('/usr/bin/python3',['-c','import sys,plistlib,json,base64,datetime\np=plistlib.loads(sys.stdin.buffer.read())\nprint(json.dumps(p,default=lambda v: base64.b64encode(v).decode() if isinstance(v,bytes) else v.isoformat()+"Z" if isinstance(v,datetime.datetime) else None))'],xml));
      const rights = validateProfile(profile,id,source.DELIDEV_APPLE_TEAM_ID,certificate);
      const original = run('/usr/bin/codesign',['--display','--entitlements',':-',path]);
      const start = original.indexOf('<?xml');
      requireValue(start >= 0, 'Original DeliDev entitlements are unavailable');
      const signedRights = run('/usr/bin/python3',['-c','import sys,plistlib,json\ne=plistlib.loads(sys.stdin.buffer.read())\ne.update(json.loads(sys.argv[1]))\nsys.stdout.buffer.write(plistlib.dumps(e))',JSON.stringify(Object.fromEntries(['com.apple.application-identifier','com.apple.developer.team-identifier'].map(k => [k,rights[k]])))],original.slice(start, original.indexOf('</plist>') + 8));
      const file = join(directory,`${id}.plist`); writeFileSync(file,signedRights,{mode:0o600});
      entitlements.set(path,file);
      copyFileSync(profileFile,join(path,'Contents/embedded.provisionprofile'));
    }
    const retainHelperEntitlements = path => {
      for (const name of readdirSync(path)) {
        const child = join(path,name);
        if (!lstatSync(child).isDirectory()) continue;
        if (name.endsWith('.app') && !entitlements.has(child)) {
          const xml = run('/usr/bin/codesign',['--display','--entitlements',':-',child]);
          if (xml.includes('<?xml')) {
            const file = join(directory,`helper-${entitlements.size}.plist`);
            writeFileSync(file,xml.slice(xml.indexOf('<?xml'),xml.indexOf('</plist>') + 8),{mode:0o600});
            entitlements.set(child,file);
          }
        }
        retainHelperEntitlements(child);
      }
    };
    retainHelperEntitlements(bundle);
    const sign = (path, entitlement) => run('/usr/bin/codesign',['--force','--sign',certificate,'--keychain',keychain,'--timestamp','--options','runtime','--generate-entitlement-der',...(entitlement ? ['--entitlements',entitlement] : []),path]);
    // Sign actual native files and bundle containers inside-out. Symlinks are
    // framework layout references, never separate executable signing targets.
    const walk = path => {
      for (const name of readdirSync(path)) {
        const child = join(path,name), stat = lstatSync(child);
        if (stat.isDirectory()) { walk(child); if (/\.(?:app|appex|framework)$/.test(name)) {
          const file = entitlements.get(child);
          sign(child,file);
        } }
        else if (stat.isFile()) {
          // Preserve original helper JIT entitlements at the container signing
          // step; individual native payloads have no new injected capabilities.
          const { openSync,readSync,closeSync } = signingFS;
          const fd = openSync(child,'r'), header = Buffer.alloc(4);
          try { readSync(fd,header,0,4,0); } finally { closeSync(fd); }
          if (['cffaedfe','cefaedfe','feedfacf','feedface','cafebabe','bebafeca'].includes(header.toString('hex'))) sign(child);
        }
      }
    };
    walk(bundle); sign(bundle,entitlements.get(bundle)); sign(worker);
    run('/usr/bin/codesign',['--verify','--deep','--strict',bundle]);
    run('/usr/bin/codesign',['--verify','--strict',worker]);
    for (const [path] of profiles) {
      const metadata = run('/usr/bin/codesign',['--display','--verbose=4',path]);
      requireValue(metadata.includes(`TeamIdentifier=${source.DELIDEV_APPLE_TEAM_ID}`) && !metadata.includes('Signature=adhoc'), 'Production DeliDev signature mismatch');
    }
    requireValue(run('/usr/libexec/PlistBuddy',['-c','Print :CFBundleShortVersionString',join(bundle,'Contents/Info.plist')]).trim() === version, 'Signed macOS version mismatch');
    const notaryKey = material('notary.p8',source.DELIDEV_APPLE_NOTARY_KEY_BASE64);
    const submit = file => {
      const result = JSON.parse(run('/usr/bin/xcrun',['notarytool','submit',file,'--key',notaryKey,'--key-id',source.DELIDEV_APPLE_NOTARY_KEY_ID,'--issuer',source.DELIDEV_APPLE_NOTARY_ISSUER_ID,'--wait','--timeout','15m','--output-format','json']));
      requireValue(result.status === 'Accepted', 'DeliDev notarization was not accepted');
    };
    const appArchive = join(directory,'app.zip');
    run('/usr/bin/ditto',['-c','-k','--keepParent',bundle,appArchive]); submit(appArchive);
    run('/usr/bin/xcrun',['stapler','staple',bundle]); run('/usr/bin/xcrun',['stapler','validate',bundle]);
    run('/usr/sbin/spctl',['--assess','--type','execute',bundle]);
    const workerArchive = join(directory,'worker.zip');
    run('/usr/bin/ditto',['-c','-k','--keepParent',worker,workerArchive]); submit(workerArchive);
    const dmgSource = join(directory,'dmg'); mkdirSync(dmgSource);
    run('/usr/bin/ditto',[bundle,join(dmgSource,'DeliDev.app')]);
    run('/usr/bin/hdiutil',['create','-fs','HFS+','-format','UDZO','-volname','DeliDev','-srcfolder',dmgSource,desktop]);
    sign(desktop); submit(desktop);
    run('/usr/bin/xcrun',['stapler','staple',desktop]); run('/usr/bin/xcrun',['stapler','validate',desktop]);
    run('/usr/bin/codesign',['--verify','--strict',desktop]);
  } finally {
    try { if (created) run('/usr/bin/security',['delete-keychain',keychain]); }
    finally { rmSync(directory,{recursive:true,force:true}); }
  }
}
import * as signingFS from 'node:fs';
