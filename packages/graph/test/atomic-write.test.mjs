import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { atomicWrite } from '../design/authority.mjs';

const fixture=()=>{const directory=fs.mkdtempSync(path.join(os.tmpdir(),'otoha-atomic-test-'));return {directory,file:path.join(directory,'state.json')};};

test('atomicWrite: creates and replaces complete JSON, flushes the file before rename',()=>{
  const {directory,file}=fixture(),originalSync=fs.fsyncSync,originalRename=fs.renameSync,events=[];
  fs.fsyncSync=fd=>{events.push(fs.fstatSync(fd).isDirectory()?'directory-sync':'file-sync');return originalSync(fd);};
  fs.renameSync=(from,to)=>{events.push('rename');return originalRename(from,to);};
  try{atomicWrite(file,'{"version":1}');assert.deepEqual(JSON.parse(fs.readFileSync(file,'utf8')),{version:1});atomicWrite(file,'{"version":2}');}
  finally{fs.fsyncSync=originalSync;fs.renameSync=originalRename;}
  assert.deepEqual(JSON.parse(fs.readFileSync(file,'utf8')),{version:2});
  const sequence=process.platform==='win32'?['file-sync','rename']:['file-sync','rename','directory-sync'];
  assert.deepEqual(events,[...sequence,...sequence]);assert.deepEqual(fs.readdirSync(directory),['state.json']);
});

for(const code of ['EPERM','ENOSPC','EACCES'])test(`atomicWrite: file fsync ${code} propagates and preserves prior state`,()=>{
  const {directory,file}=fixture();fs.writeFileSync(file,'old');const original=fs.fsyncSync;
  fs.fsyncSync=()=>{throw Object.assign(new Error('injected file sync failure'),{code});};
  try{assert.throws(()=>atomicWrite(file,'new'),e=>e.code===code);}finally{fs.fsyncSync=original;}
  assert.equal(fs.readFileSync(file,'utf8'),'old');assert.deepEqual(fs.readdirSync(directory),['state.json']);
});

test('atomicWrite: rename EACCES propagates and preserves prior state',()=>{
  const {directory,file}=fixture();fs.writeFileSync(file,'old');const original=fs.renameSync;
  fs.renameSync=()=>{throw Object.assign(new Error('injected replacement failure'),{code:'EACCES'});};
  try{assert.throws(()=>atomicWrite(file,'new'),e=>e.code==='EACCES');}finally{fs.renameSync=original;}
  assert.equal(fs.readFileSync(file,'utf8'),'old');assert.deepEqual(fs.readdirSync(directory),['state.json']);
});

test('atomicWrite: POSIX directory sync errors still propagate',{skip:process.platform==='win32'},()=>{
  const {file}=fixture(),original=fs.fsyncSync;
  fs.fsyncSync=fd=>{if(fs.fstatSync(fd).isDirectory())throw Object.assign(new Error('directory flush failed'),{code:'EIO'});return original(fd);};
  try{assert.throws(()=>atomicWrite(file,'new'),e=>e.code==='EIO');}finally{fs.fsyncSync=original;}
});
