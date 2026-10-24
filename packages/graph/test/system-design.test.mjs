import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { spawn } from 'node:child_process';
import vm from 'node:vm';
import { validateModel, loadModel, modelSnapshot, compileView, digest, evaluateBinding } from '../design/model.mjs';
import { mutateRequest, readRequests } from '../design/requests.mjs';
import { deliverDesign } from '../design/deliver.mjs';
import { startDesignPreview } from '../design/server.mjs';

import { fixture } from './helpers/system-fixture.mjs';

test('model: stable identity, containment and relation kinds remain distinct; invalid graphs fail',()=>{
 const f=fixture();assert.deepEqual(validateModel(f.model),[]);
 const graph=compileView(f.model,f.model.views[1]);assert.equal(graph.connections.length,0);assert.deepEqual(graph.components.map(c=>c.id),['asr','music']);
 for(const mutate of [m=>m.entities[1].parent='asr',m=>m.entities[0].id='parser',m=>m.views[1].placements=[],m=>m.entities[0].maturity.verification={value:'passed'},m=>m.relations[0].kind='contains',m=>m.evidence[1].inputs[0].path='unrelated.mjs']){const bad=structuredClone(f.model);mutate(bad);assert.ok(validateModel(bad).length);}
});
test('migration: cross-cutting views preserve identity and authored presentation without inventing containment',()=>{
 const {model}=fixture();
 model.entities.push({...structuredClone(model.entities.find(e=>e.id==='music')),id:'spool',label:'Persistent audio'});
 const perspective={id:'ingress',kind:'perspective',entryPoints:['parser'],title:'Audio ingress',placements:[{entity:'input',pos:[40,180]},{entity:'parser',pos:[370,180],label:'Parser in this path',sublabel:'Scope-specific wording',tag:'Candidate only',type:'security'},{entity:'spool',pos:[700,180]}],relations:[{relation:'input-parser',variant:'emphasis',labelDy:24}],cards:[{dot:'amber',title:'Evidence boundary',items:['HTTP arrival is not a rendered frame.']}],guidedViews:[{id:'audio-path',label:'Audio path',focus:['input','parser'],note:'A reading path, not containment.'}]};
 model.views.push(perspective);assert.deepEqual(validateModel(model),[]);
 const compiled=compileView(model,perspective);assert.equal(compiled.components[1].tag,'Candidate only');assert.equal(compiled.components[1].label,'Parser in this path');assert.equal(model.entities.find(e=>e.id==='parser').label,'parser');assert.equal(compiled.connections[0].variant,'emphasis');assert.equal(compiled.connections[0].labelDy,24);assert.equal(model.relations[0].kind,'dataflow');assert.deepEqual(compiled.cards,perspective.cards);assert.deepEqual(compiled.meta.views,perspective.guidedViews);
 for(const mutate of [m=>m.views.at(-1).entryPoints=['missing'],m=>m.views.at(-1).scope='parser',m=>m.views.at(-1).guidedViews[0].focus=['music'],m=>m.views.at(-1).entryPoints=[]]){const bad=structuredClone(model);mutate(bad);assert.ok(validateModel(bad).length);}
});
test('evidence: changed input invalidates only bound claims; symlink escape is unavailable',()=>{
 const f=fixture();const before=modelSnapshot(loadModel(f.input),f.root);assert.equal(before.model.entities.find(e=>e.id==='decode').maturity.verification.effective,'passed');
 fs.writeFileSync(path.join(f.root,'implementation.mjs'),'export const result = 2;\n');const after=modelSnapshot(loadModel(f.input),f.root);
 assert.equal(after.model.entities.find(e=>e.id==='decode').maturity.verification.effective,'unknown');assert.equal(after.model.entities.find(e=>e.id==='music').maturity.verification.effective,'untested');
 fs.symlinkSync('/etc/hosts',path.join(f.root,'escape'));assert.equal(evaluateBinding({path:'escape',sha256:'0'.repeat(64)},f.root).status,'unavailable');
});
test('requests: persistence, CAS, idempotency, evidence and honest stages',async()=>{
 const f=fixture();const create={action:'create',operationId:'create-1',id:'request-1',entityId:'decode',baseRevision:f.revision,text:'Improve the isolated fixture'};
 assert.equal((await mutateRequest(f.options,create)).request.stage,'submitted');assert.equal((await mutateRequest(f.options,create)).replayed,true);
 await assert.rejects(mutateRequest(f.options,{...create,text:'changed'}),e=>e.code==='request/idempotency-conflict');
 const report=(stage,version,evidence=[])=>({action:'report',operationId:stage+'-'+version,id:create.id,baseRevision:f.revision,expectedVersion:version,actor:'test-agent',stage,evidence});
 await assert.rejects(mutateRequest(f.options,report('tests_passed',1,[f.testEvidence])),e=>e.code==='request/transition');
 assert.equal((await mutateRequest(f.options,report('accepted',1))).request.stage,'accepted');
 await assert.rejects(mutateRequest(f.options,report('implemented',1,[f.source])),e=>e.code==='request/version-conflict');
 await mutateRequest(f.options,report('implemented',2,[f.source]));await mutateRequest(f.options,report('tests_passed',3,[f.testEvidence]));
 let requests=readRequests(f.options);assert.equal(requests.length,1);assert.equal(requests[0].stage,'tests_passed');assert.equal(requests[0].stale,false);
 fs.writeFileSync(path.join(f.root,'implementation.mjs'),'export const result = 3;');requests=readRequests(f.options);assert.equal(requests[0].stale,true);
 await assert.rejects(mutateRequest(f.options,report('integrated',4,[{...f.testEvidence,kind:'runtime'}])),e=>e.code==='request/stale-evidence');
 const freshSource={...f.source,sha256:digest(fs.readFileSync(path.join(f.root,'implementation.mjs')))};
 await mutateRequest(f.options,report('implemented',4,[freshSource]));assert.equal(readRequests(f.options)[0].receipts.some(r=>r.stage==='tests_passed'),false);
});
test('requests: independent processes serialize one duplicate operation; stale design conflicts preserve data',async()=>{
 const f=fixture();const payload={action:'create',operationId:'duplicate',id:'request-2',entityId:'parser',baseRevision:f.revision,text:'One request'};
 const payloadFile=path.join(f.root,'request.json');fs.writeFileSync(payloadFile,JSON.stringify(payload));
 const cli=new URL('../bin/archify.mjs',import.meta.url);
 const run=()=>new Promise((resolve,reject)=>{const child=spawn(process.execPath,[fileURLToPath(cli),'design','submit',f.input,'--payload',payloadFile]);let out='',err='';child.stdout.on('data',x=>out+=x);child.stderr.on('data',x=>err+=x);child.on('error',reject);child.on('close',code=>code?reject(Error(err)):resolve(JSON.parse(out)));});
 const results=await Promise.all([run(),run()]);assert.equal(results.filter(r=>!r.replayed).length,1);assert.equal(readRequests(f.options).length,1);
 f.model.meta.title='New design';fs.writeFileSync(f.input,JSON.stringify(f.model));await assert.rejects(mutateRequest(f.options,{...payload,id:'new',operationId:'new'}),e=>e.code==='request/revision-conflict');
 assert.equal(readRequests(f.options).length,1);
});
test('delivery and preview: existing renderer receipts, last good, content drift and HTTP mutation boundary', {timeout:30000},async()=>{
 const f=fixture();const output=path.join(f.root,'system.html');const delivered=deliverDesign(f.input,output,f.options);assert.equal(delivered.receipt.views.length,3);assert.ok(fs.readFileSync(output,'utf8').includes('system-data'));
 const scripts=[...delivered.html.matchAll(/<script>([\s\S]*?)<\/script>/g)];assert.equal(scripts.length,1);assert.doesNotThrow(()=>new vm.Script(scripts[0][1]));
 const before=digest(fs.readFileSync(output));const server=await startDesignPreview({...f.options,pollMs:40});
 try{
  const state=await(await fetch(server.url+'api/state')).json();assert.equal(state.revision,f.revision);
  const session=await(await fetch(server.url+'api/session')).json();const data={action:'create',operationId:'http-1',id:'http-request',entityId:'parser',baseRevision:f.revision,text:'Saved from browser'};
  assert.equal((await fetch(server.url+'api/requests',{method:'POST',body:JSON.stringify(data)})).status,403);
  assert.equal((await fetch(server.url+'api/requests',{method:'POST',headers:{Origin:server.url.slice(0,-1),'Content-Type':'application/json','X-Archify-Token':session.token},body:JSON.stringify(data)})).status,200);
  fs.writeFileSync(f.input,'{"unfinished":');await new Promise(r=>setTimeout(r,100));const bad=await(await fetch(server.url+'api/state')).json();assert.ok(bad.failure);assert.equal(bad.revision,f.revision);
  assert.throws(()=>deliverDesign(f.input,output,f.options));assert.equal(digest(fs.readFileSync(output)),before);
  fs.writeFileSync(f.input,JSON.stringify(f.model));await new Promise(r=>setTimeout(r,700));assert.equal((await(await fetch(server.url+'api/state')).json()).failure,null);
  fs.writeFileSync(path.join(f.root,'implementation.mjs'),'drift');const drift=await(await fetch(server.url+'api/state')).json();assert.equal(drift.model.evidence.find(e=>e.id==='code').status,'stale');
 }finally{await server.stop();}
});
test('failed checks remain blocked, and a hash-matching unrelated test cannot advance a request',async()=>{
 const f=fixture();await mutateRequest(f.options,{action:'create',operationId:'fail-create',id:'failing-request',entityId:'decode',baseRevision:f.revision,text:'Verify failure behavior'});
 const report=(stage,version,evidence=[],note='')=>({action:'report',operationId:stage+'-'+version,id:'failing-request',baseRevision:f.revision,expectedVersion:version,actor:'failure-test',stage,evidence,note});
 await mutateRequest(f.options,report('accepted',1));await mutateRequest(f.options,report('implemented',2,[f.source]));
 await assert.rejects(mutateRequest(f.options,report('tests_passed',3,[{...f.testEvidence,result:'failed'}])),e=>e.code==='request/evidence');
 await assert.rejects(mutateRequest(f.options,report('tests_passed',3,[{...f.testEvidence,inputs:[{path:'unrelated.mjs',sha256:f.source.sha256}]}])),e=>e.code==='request/input-binding');
 await mutateRequest(f.options,report('blocked',3,[{...f.testEvidence,result:'failed'}],'The scoped check failed'));
 assert.equal(readRequests(f.options)[0].stage,'blocked');assert.equal(readRequests(f.options)[0].receipts.at(-1).evidence[0].result,'failed');
});

test('native file callback accepts only bound evidence with same-origin token; unavailable is explicit',async()=>{
 const f=fixture(),opened=[];
 const server=await startDesignPreview({...f.options,openCodexFile:async target=>opened.push(target)});
 try{
  const session=await(await fetch(server.url+'api/session')).json();assert.equal(session.codexFiles,true);
  const headers={Origin:server.url.slice(0,-1),'Content-Type':'application/json','X-Archify-Token':session.token};
  const post=(payload,h=headers)=>fetch(server.url+'api/open-in-codex',{method:'POST',headers:h,body:JSON.stringify(payload)});
  assert.equal((await post({evidenceId:'code'},{})).status,403);
  assert.equal((await post({evidenceId:'code'},{...headers,Origin:'http://other.example'})).status,403);
  assert.equal((await post({evidenceId:'code',path:'/etc/hosts'})).status,400);
  assert.equal((await post({evidenceId:'missing'})).status,400);
  assert.deepEqual(opened,[]);
  assert.equal((await post({evidenceId:'code'})).status,200);
  assert.deepEqual(opened,[{path:fs.realpathSync(path.join(f.root,'implementation.mjs')),line:1}]);
  const source=await(await fetch(server.url+'api/source?id=code')).json();assert.equal(source.absolutePath,opened[0].path);
 }finally{await server.stop();}
 const unavailable=await startDesignPreview({...f.options,openCodexFile:null});
 try{assert.equal((await(await fetch(unavailable.url+'api/session')).json()).codexFiles,false);}finally{await unavailable.stop();}
});
