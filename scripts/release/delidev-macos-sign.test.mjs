// SPDX-License-Identifier: Apache-2.0
import assert from 'node:assert/strict';
import test from 'node:test';
import { createHash } from 'node:crypto';
import { existsSync, mkdtempSync, mkdirSync, writeFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { macSecrets, validateSigningConfiguration, validateProfile, signMacOS } from './delidev-macos-sign.mjs';
const cert=Buffer.from('fixture certificate'), team='ABCDE12345';
const thumb=createHash('sha1').update(cert).digest('hex').toUpperCase();
const config=Object.fromEntries(macSecrets.map(n=>[n,Buffer.from('fixture').toString('base64')]));
Object.assign(config,{DELIDEV_APPLE_TEAM_ID:team,DELIDEV_MACOS_CERTIFICATE_SHA1:thumb,DELIDEV_APPLE_NOTARY_KEY_ID:'12345ABCDE',DELIDEV_APPLE_NOTARY_ISSUER_ID:'12345678-1234-1234-1234-123456789abc',DELIDEV_MACOS_CERTIFICATE_PASSWORD:'fixture-private-password'});
const profile=id=>({TeamIdentifier:[team],ExpirationDate:'2099-01-01T00:00:00Z',DeveloperCertificates:[cert.toString('base64')],ProvisionsAllDevices:true,Entitlements:{'com.apple.application-identifier':`${team}.${id}`,'com.apple.developer.team-identifier':team,'com.apple.security.application-groups':['group.io.delino.delidev']}});
const xml='<?xml version="1.0"?><plist version="1.0"><dict><key>com.apple.security.application-groups</key><array><string>group.io.delino.delidev</string></array></dict></plist>';
test('signing configuration fails before material access for every missing credential',()=>{
  validateSigningConfiguration(config);
  for(const name of [...macSecrets,'DELIDEV_APPLE_TEAM_ID','DELIDEV_MACOS_CERTIFICATE_SHA1']) assert.throws(()=>validateSigningConfiguration({...config,[name]:''}));
});
test('profiles retain exact team, bundle ID, App Group, certificate, expiry and Developer ID authority',()=>{
  const id='io.delino.delidev.widget', value=profile(id);
  validateProfile(value,id,team,thumb);
  for(const changed of [{TeamIdentifier:['other']},{ExpirationDate:'2020-01-01T00:00:00Z'},{DeveloperCertificates:[]},{ProvisionsAllDevices:false},{Entitlements:{...value.Entitlements,'get-task-allow':true}},{Entitlements:{...value.Entitlements,'com.apple.application-identifier':`${team}.other`}},{Entitlements:{...value.Entitlements,'com.apple.security.application-groups':['another']}}]) assert.throws(()=>validateProfile({...value,...changed},id,team,thumb));
});
for(const fail of [null,'import','codesign','submit','staple','cancel']) test(`signing ${fail ?? 'success'} retains helper entitlements and cleans original keychain/material`,async t=>{
  const folder=mkdtempSync(join(tmpdir(),'delidev-sign-test-'));t.after(()=>rmSync(folder,{recursive:true,force:true}));
  const bundle=join(folder,'DeliDev.app'),worker=join(folder,'worker'),desktop=join(folder,'desktop.dmg');
  for(const child of ['Contents/MacOS','Contents/PlugIns/DeliDevWidget.appex/Contents','Contents/PlugIns/DeliDevWidgetSelection.appex/Contents','Contents/Frameworks/Helper.app/Contents/MacOS'])mkdirSync(join(bundle,child),{recursive:true});
  writeFileSync(join(bundle,'Contents/MacOS/main'),Buffer.from('cffaedfe','hex'));writeFileSync(worker,Buffer.from('cffaedfe','hex'));
  const calls=[];let privateDirectory;
  const runCommand=(program,args,input,env)=>{
    assert.ok(!Object.keys(env).some(key=>key.startsWith('DELIDEV_')));
    calls.push([program,args]);
    if(fail==='cancel' && args[0]==='import') process.emit('SIGTERM');
    if(args[0]==='create-keychain')privateDirectory=args.at(-1).replace('/release.keychain-db','');
    if(fail==='import'&&args[0]==='import'||fail==='codesign'&&args.includes('--force')||fail==='submit'&&args.includes('submit')||fail==='staple'&&args.includes('staple'))throw new Error('fixture failure');
    if(args[0]==='find-identity')return `${thumb} Developer ID Application: fixture`;
    if(args[0]==='cms')return args.at(-1).split('/').at(-1).replace('.provisionprofile','');
    if(program.endsWith('python3'))return args[1].includes('base64')?JSON.stringify(profile(input)):xml;
    if(args.includes('--display'))return args.includes('--entitlements')?xml:`TeamIdentifier=${team}\nAuthority=Developer ID Application: fixture`;
    if(program.endsWith('PlistBuddy'))return '0.1.1';
    if(args.includes('submit'))return JSON.stringify({status:'Accepted'});
    return '';
  };
  const action=()=>signMacOS({bundle,worker,desktop,version:'0.1.1',source:{...config,SECRET_TOKEN:'must-not-leak'},runCommand});
  if(fail)await assert.rejects(action,fail==='cancel' ? /canceled/ : /fixture failure/);else await action();
  assert.equal(existsSync(privateDirectory),false);
  assert.ok(calls.some(([,args])=>args[0]==='delete-keychain'));
  if(!fail){
    const helperSign=calls.find(([,args])=>args.includes('--force')&&args.at(-1).endsWith('Helper.app'));
    assert.ok(helperSign[1].includes('--entitlements'));
    assert.equal(calls.filter(([,args])=>args.includes('submit')).length,3);
    assert.ok(calls.some(([,args])=>args[0]==='--assess'));
  }
});
