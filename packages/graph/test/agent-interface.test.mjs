import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { spawn } from 'node:child_process';
import { fixture } from './helpers/system-fixture.mjs';
import { modelSnapshot, loadModel, digest } from '../design/model.mjs';
import { queryGraph, diffSnapshots, topologyHash, assertProjectionCoverage, viewGraph } from '../design/query.mjs';
import { GraphAuthority, authorityDirectory, sourceFingerprint } from '../design/authority.mjs';
import { startDesignPreview } from '../design/server.mjs';
import { readAuthority } from '../design/client.mjs';

const snapshot=f=>({...modelSnapshot(loadModel(f.input),f.root),cursor:1});
const values=(r,type)=>r.records.filter(r=>r.type===type).map(r=>r.value);
const ids=(r,type)=>values(r,type).map(v=>v.id).sort();
const get=async(s,route)=>{const r=await fetch(s.url+route);return {status:r.status,body:await r.json()};};
const write=f=>fs.writeFileSync(f.input,JSON.stringify(f.model));
const cli=(...args)=>new Promise((resolve,reject)=>{const p=spawn(process.execPath,[fileURLToPath(new URL('../bin/otoha-graph.mjs',import.meta.url)),...args]);let out='',err='';p.stdout.on('data',x=>out+=x);p.stderr.on('data',x=>err+=x);p.on('error',reject);p.on('close',code=>code?reject(Error(err)):resolve(JSON.parse(out)));});

test('queries: local boundaries, explicit containment depth, reach and path keep different semantics',()=>{
  const f=fixture(),s=snapshot(f);
  const local=queryGraph(s,{mode:'local',target:'parser',hops:0,depth:1});
  assert.deepEqual(ids(local,'entity'),['asr','music','parser']);assert.deepEqual(ids(local,'boundary'),['input-parser','parser-session']);assert.deepEqual(ids(local,'relation'),[]);
  const reach=queryGraph(s,{mode:'reach',target:'input'});assert.deepEqual(ids(reach,'entity'),['input','parser','session']);assert.ok(!ids(reach,'entity').includes('decode'));
  const upstream=queryGraph(s,{mode:'reach',target:'session',direction:'in'});assert.deepEqual(ids(upstream,'entity'),ids(reach,'entity'));
  assert.deepEqual(queryGraph(s,{mode:'path',from:'input',to:'session'}).path,['input','parser','session']);
  assert.deepEqual(queryGraph(s,{mode:'path',from:'input',to:'decode'}).path,[]);
  assert.deepEqual(queryGraph(s,{mode:'path',from:'input',to:'input'}).path,['input']);
  const macro=queryGraph(s,{mode:'overview'});assert.deepEqual(ids(macro,'entity'),['input','parser','session']);assert.deepEqual(values(macro,'entity').find(e=>e.id==='parser').aggregation.memberIds,['asr','decode','emit','music','parser']);
  assert.deepEqual(values(macro,'relation').flatMap(e=>e.sourceRelationIds).sort(),['input-parser','parser-session']);
  assert.throws(()=>queryGraph(s,{mode:'local',target:'absent'}),e=>e.code==='query/entity');
  assert.throws(()=>queryGraph(s,{mode:'full',runtimeAt:10}),e=>e.code==='query/argument');
  const expanded=queryGraph(s,{mode:'view',view:'overview',expanded:'parser,parser/asr'});
  assert.deepEqual(ids(expanded,'entity'),f.model.entities.map(e=>e.id).sort());assert.deepEqual(ids(expanded,'relation'),f.model.relations.map(e=>e.id).sort());
  assert.deepEqual(ids(queryGraph(s,{mode:'view',view:'overview',expanded:'parser/asr'}),'entity'),['input','parser','session']);
});

test('queries: cyclic regions, parallel edge kinds, self loops and one shortest witness',()=>{
  const f=fixture(),s=snapshot(f);s.model.relations.push({id:'return',from:'session',to:'input',kind:'feedback',label:'feedback'},{id:'parallel',from:'input',to:'parser',kind:'call',label:'call'},{id:'self',from:'music',to:'music',kind:'feedback',label:'self'});
  const r=queryGraph(s,{mode:'cycles'});assert.deepEqual(values(r,'group').map(g=>g.members).sort(),[['input','parser','session'],['music']]);
  assert.equal(values(queryGraph(s,{mode:'cycles',kinds:'dataflow'}),'group').length,0);
  assert.deepEqual(ids(queryGraph(s,{mode:'cycles',target:'music'}),'entity'),['music']);
  assert.equal(values(queryGraph(s,{mode:'path',from:'input',to:'parser'}),'relation').length,1);
});

test('queries: pagination covers all records exactly once, pins query/version, and full includes evidence',()=>{
  const f=fixture(),s=snapshot(f),q={mode:'full',detail:'full',limit:2};let page,records=[];
  do{const r=queryGraph(s,{...q,page});assert.equal(r.complete,false);records.push(...r.records);page=r.page.next;}while(page);
  assert.deepEqual(records,queryGraph(s,{mode:'full',detail:'full'}).records);
  assert.equal(records.filter(r=>r.type==='evidence').length,2);
  const first=queryGraph(s,q);assert.throws(()=>queryGraph({...s,cursor:2},{...q,page:first.page.next}),e=>e.code==='query/page');
  assert.throws(()=>queryGraph(s,{...q,detail:'summary',page:first.page.next}),e=>e.code==='query/page');
  s.model.entities.find(e=>e.id==='asr').purpose='x'.repeat(4000);assert.throws(()=>queryGraph(s,{mode:'full',detail:'full',maxBytes:1024}),e=>e.code==='query/item-too-large');
});

test('diff: stable IDs distinguish additions, deletions, field edits, metadata and evidence',()=>{
  const a=snapshot(fixture()),b=structuredClone(a);b.cursor=2;b.revision='new';b.model.entities[0].label='changed';b.model.entities.pop();b.model.relations.push({id:'new',from:'input',to:'session',kind:'call',label:'new'});b.model.meta.title='changed';b.model.evidence[0].status='stale';
  const diff=diffSnapshots(a,b);assert.ok(diff.records.some(x=>x.collection==='entities'&&x.id==='emit'&&x.operation==='removed'));assert.ok(diff.records.some(x=>x.id==='input'&&x.operation==='updated'));assert.ok(diff.records.some(x=>x.id==='new'&&x.operation==='added'));assert.ok(diff.records.some(x=>x.collection==='meta'));assert.ok(diff.records.some(x=>x.collection==='evidence'));
  const reconstructed=structuredClone(a.model);for(const change of diff.records){if(change.collection==='meta'){reconstructed.meta=change.after;continue;}const items=reconstructed[change.collection],i=items.findIndex(x=>x.id===change.id);if(change.operation==='removed')items.splice(i,1);else if(i<0)items.push(change.after);else items[i]=change.after;}
  assert.deepEqual(reconstructed,b.model);
});

test('authority: HTTP, CLI, rendered topology, pinned bundle and source-invalid fallback agree',async()=>{
  const f=fixture(),s=await startDesignPreview({...f.options,pollMs:60000});
  try{
    const manifest=await cli('manifest',f.input);assert.equal(manifest.connection,'live');assert.equal(manifest.cursor,1);
    const bundle=(await get(s,'api/bundle?cursor=1')).body;
    for(const v of f.model.views){
      const q=await cli('query',f.input,'--mode','view','--view',v.id,'--cursor','1');const g=viewGraph(f.model,v.id);
      assert.deepEqual(ids(q,'entity'),g.entities.map(x=>x.id).sort());assert.deepEqual(ids(q,'relation'),g.relations.map(x=>x.id).sort());
      const html=bundle.views[v.id].replace(/<script[\s\S]*?<\/script>/g,''),renderedNodes=[...new Set([...html.matchAll(/data-node-id="([^"]+)"/g)].map(m=>m[1]))].sort(),renderedEdges=[...new Set([...html.matchAll(/data-edge-id="([^"]+)"/g)].map(m=>m[1]))].sort();
      assert.deepEqual(renderedNodes,ids(q,'entity'));assert.deepEqual(renderedEdges,ids(q,'relation'));
    }
    fs.writeFileSync(f.input,'{"broken":');s.authority.refresh();
    const bad=(await get(s,'api/state')).body,agent=await cli('query',f.input,'--mode','full');assert.ok(bad.failure);assert.equal(agent.cursor,bad.cursor);assert.equal(agent.revision,bad.revision);assert.equal(bad.cursor,1);
    f.model.meta.title='Updated';write(f);s.authority.refresh();assert.equal(s.authority.current.cursor,2);
    assert.equal((await get(s,'api/bundle?cursor=1')).body.snapshot.model.meta.title,'System fixture');
    assert.equal((await get(s,'api/bundle?cursor=2')).body.snapshot.model.meta.title,'Updated');
    const firstHistory=s.authority.history({limit:1});
    f.model.meta.title='Third';write(f);s.authority.refresh();
    const nextHistory=s.authority.history({limit:1,cursor:firstHistory.cursor,page:firstHistory.page.next});
    assert.deepEqual(nextHistory.records.map(r=>r.cursor),[2]);assert.equal(nextHistory.page.hasMore,false);assert.equal(nextHistory.cursor,2);
    assert.equal((await get(s,'api/query?cursor=999')).status,409);
    assert.ok((await get(s,'api/diff?after=1&cursor=2')).body.records.some(x=>x.collection==='meta'));
    assert.equal((await get(s,'api/events?after=999')).status,409);
    const invisible=structuredClone(f.model);invisible.views[0].relations=[];assert.throws(()=>assertProjectionCoverage(invisible),e=>e.code==='graph/unprojected');
  }finally{await s.stop();}
});

test('authority: restart with missing/broken source, offline reads, CAS rollback and idempotent retry',async()=>{
  const f=fixture();let s=await startDesignPreview(f.options);
  f.model.meta.title='Second';write(f);s.authority.refresh();const second=s.authority.current.cursor;await s.stop();
  fs.writeFileSync(f.input,'incomplete');s=await startDesignPreview(f.options);
  try{
    assert.equal(s.authority.current.cursor,second);assert.ok(s.authority.failure);
    const status=s.authority.status(),payload={cursor:1,expectedCursor:status.cursor,expectedSourceHash:status.sourceHash,operationId:'restore-1'};
    const r=s.authority.rollback(payload);assert.equal(r.cursor,3);assert.equal(JSON.parse(fs.readFileSync(f.input)).meta.title,'System fixture');assert.equal(fs.readFileSync(r.sourceBackup,'utf8'),'incomplete');
    assert.equal(s.authority.rollback(payload).replayed,true);
    assert.throws(()=>s.authority.rollback({...payload,operationId:'stale'}),e=>e.code==='authority/conflict');
    const fresh=s.authority.status();fs.writeFileSync(f.input,'external edit');assert.throws(()=>s.authority.rollback({...payload,expectedCursor:fresh.cursor,expectedSourceHash:fresh.sourceHash,operationId:'external'}),e=>e.code==='authority/conflict');
  }finally{await s.stop();}
  const offline=await readAuthority({...f.options,offline:true},'query',{mode:'overview'});assert.equal(offline.connection,'offline-accepted-snapshot');assert.equal(offline.cursor,3);
  fs.unlinkSync(f.input);s=await startDesignPreview(f.options);assert.equal(s.authority.current.cursor,3);await s.stop();
});

test('authority: one writer, dead lock recovery, corrupted journal preserves readable prefix and blocks writes',async()=>{
  const f=fixture();let s=await startDesignPreview(f.options);
  await assert.rejects(startDesignPreview(f.options),e=>e.code==='authority/locked');
  await assert.rejects(startDesignPreview({...f.options,stateDir:path.join(f.root,'another-store')}),e=>e.code==='authority/locked');
  f.model.meta.title='Later';write(f);s.authority.refresh();await s.stop();
  const dir=authorityDirectory(f.options);fs.writeFileSync(path.join(dir,'writer.lock'),JSON.stringify({pid:99999999,token:'dead'}));
  s=await startDesignPreview(f.options);assert.equal(s.authority.current.cursor,2);await s.stop();
  fs.writeFileSync(path.join(dir,'commits','000000000002.json'),'corrupt');
  s=await startDesignPreview(f.options);try{assert.equal(s.authority.current.cursor,1);assert.equal(s.authority.status().readOnly,true);assert.equal(s.authority.snapshot().model.meta.title,'System fixture');assert.throws(()=>s.authority.rollback({}),e=>e.code==='authority/store-damaged');assert.equal(fs.readFileSync(path.join(dir,'commits','000000000002.json'),'utf8'),'corrupt');}finally{await s.stop();}
});

test('authority: custom storage is discovered for live and durable offline reads',async()=>{
  const f=fixture(),stateDir=path.join(f.root,'custom-store');const s=await startDesignPreview({...f.options,stateDir});
  try{assert.equal((await cli('manifest',f.input)).connection,'live');assert.ok(fs.existsSync(path.join(stateDir,'authority','.gitignore')));}
  finally{await s.stop();}
  const offline=await cli('query',f.input,'--offline','--mode','full');assert.equal(offline.cursor,1);assert.equal(offline.connection,'offline-accepted-snapshot');assert.equal(ids(offline,'entity').length,f.model.entities.length);
});

test('authority: event resume, evidence-only versions and partial writes never become published graphs',async()=>{
  const f=fixture(),s=await startDesignPreview({...f.options,pollMs:60000}),controller=new AbortController();
  try{
    const response=await fetch(s.url+'api/events?after=1',{signal:controller.signal}),reader=response.body.getReader();await reader.read();
    fs.writeFileSync(path.join(f.root,'implementation.mjs'),'changed');s.authority.refresh({forceEvidence:true});
    assert.equal(s.authority.current.cursor,2);assert.equal(s.authority.snapshot().revision,f.revision);
    const resumed=await fetch(s.url+'api/events?after=1',{signal:controller.signal}),secondReader=resumed.body.getReader();const chunk=await secondReader.read();assert.match(new TextDecoder().decode(chunk.value),/"cursor":2/);
    assert.equal(s.authority.snapshot().model.entities.find(e=>e.id==='decode').maturity.verification.effective,'unknown');
    const before=s.authority.current.cursor;fs.writeFileSync(f.input,'{');s.authority.refresh();assert.equal(s.authority.current.cursor,before);
    write(f);s.authority.refresh();assert.equal(s.authority.failure,null);
    assert.equal(topologyHash(s.authority.snapshot().model),topologyHash(f.model));
  }finally{controller.abort();await s.stop();}
});
