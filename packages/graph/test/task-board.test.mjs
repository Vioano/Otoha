import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { execFileSync, execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { fixture } from './helpers/system-fixture.mjs';
import { parseModel, modelSnapshot, digest } from '../design/model.mjs';
import { queryGraph, diffSnapshots, topologyHash } from '../design/query.mjs';
import { projectTasks, changeTask } from '../design/tasks.mjs';
import { GraphAuthority } from '../design/authority.mjs';
import { startDesignPreview } from '../design/server.mjs';
import { initLeader, TeamLeader } from '../team/leader.mjs';
import { initMember, TeamMember } from '../team/member.mjs';
import { installOtohaBoard } from '../design/board.mjs';

const task=(id='first',extra={})=>({id,title:'检查输入',status:'todo',assignees:['alice'],entities:['parser'],description:'定义数据口径',acceptance:['一致的单位'],deliverables:[],blocked:'',...extra});
const setup=()=>{const f=fixture();f.model.tasks=[task(),task('second',{entities:['parser','asr'],assignees:['bob'],status:'doing'})];fs.writeFileSync(f.input,JSON.stringify(f.model));return f;};
const snapshot=f=>({...modelSnapshot(parseModel(JSON.stringify(f.model)),f.root),cursor:1});
const mutate=(a,data)=>changeTask(a,data,(...args)=>a.transact(...args));

test('tasks: restart remembers explicit evidence root and still detects changed evidence',()=>{
 const f=setup();let a=new GraphAuthority(f.options);a.acquire();a.refresh();
 const verification=()=>a.snapshot().model.entities.find(e=>e.id==='decode').maturity.verification.effective;
 try{
  assert.equal(verification(),'passed');a.close();a=new GraphAuthority({input:f.input});a.acquire();a.refresh({forceEvidence:true});
  assert.equal(a.options.repoRoot,f.root);assert.equal(verification(),'passed');
  mutate(a,{operationId:'after-restart',expectedCursor:a.record().cursor,action:'update',taskId:'first',patch:{status:'doing'}});
  assert.equal(verification(),'passed');
  const object=a.loadObject(a.record().object);assert.equal(Object.hasOwn(object,'repoRoot'),false);
  fs.writeFileSync(path.join(f.root,'implementation.mjs'),'export const result = 2;\n');a.refresh({forceEvidence:true});
  assert.notEqual(verification(),'passed');
  a.close();fs.writeFileSync(path.join(a.directory,'context.json'),'broken context');
  assert.throws(()=>new GraphAuthority({input:f.input}),e=>e.code==='authority/context');
 }finally{a.close();}
});

test('tasks: leader CLI uses its private authority and team board filters work for both roles',async()=>{
 const f=setup(),root=fs.mkdtempSync(path.join(os.tmpdir(),'otoha-task-cli-')),remote=path.join(root,'remote.git');execFileSync('git',['init','--bare','--quiet',remote]);
 const directory=path.join(root,'leader'),memberDir=path.join(root,'member');
 const invitation=initLeader({directory,input:f.input,repoRoot:f.root,projectId:'tasks',remote});initMember({directory:memberDir,actor:'alice',invitation});
 const leader=new TeamLeader(directory),member=new TeamMember(memberDir),run=promisify(execFile);
 let server;
 const cli=async(...args)=>JSON.parse((await run(process.execPath,['bin/otoha-graph.mjs',...args])).stdout);
 try{
  await assert.rejects(startDesignPreview({...f.options,team:leader}),e=>e.code==='authority/identity');
  server=await startDesignPreview({input:leader.input,team:leader,repoRoot:f.root,pollMs:60000});
  assert.equal((await cli('status',leader.input)).connection,'live');
  assert.equal((await cli('query',leader.input,'--mode','board','--assignee','alice')).records.length,1);
  const payload=path.join(root,'change.json');fs.writeFileSync(payload,JSON.stringify({operationId:'leader-cli-move',expectedCursor:leader.authority.record().cursor,action:'update',taskId:'first',patch:{status:'review'}}));
  assert.equal((await cli('task',leader.input,'--payload',payload)).ok,true);
  assert.equal(JSON.parse(fs.readFileSync(f.input)).tasks[0].status,'todo');
  member.accept(leader.publication());
  for(const dir of [directory,memberDir]){
   const q=await cli('team','query','--state',dir,'--mode','board','--assignee','alice','--status','review','--search','口径');
   assert.deepEqual(q.records.map(r=>r.value.id),['first']);
   assert.equal(q.connection,dir===directory?'live':'local-accepted-snapshot');
  }
   assert.equal((await cli('team','state','--state',directory)).connection,'live');
  const cached=await cli('team','state','--state',memberDir);
  assert.equal(cached.connection,'local-accepted-snapshot');assert.match(cached.warning,/does not synchronize/);
  assert.equal((await cli('team','manifest','--state',memberDir)).connection,'local-accepted-snapshot');
  await server.stop();server=null;
  const offline=await cli('team','state','--state',directory);
  assert.equal(offline.connection,'offline-accepted-snapshot');assert.ok(offline.warning);
 }finally{if(server)await server.stop();leader.close();member.close();}
});

test('tasks: legacy compatibility, stable identities and dangling module references are validated',()=>{
 const legacy=fixture();assert.doesNotThrow(()=>parseModel(JSON.stringify(legacy.model)));
 const f=setup();assert.doesNotThrow(()=>parseModel(JSON.stringify(f.model)));
 for(const change of [m=>m.tasks.push(task()),m=>m.tasks[0].entities.push('missing'),m=>m.tasks[0].status='passed',m=>m.tasks[0].maturity={verification:'passed'},m=>m.tasks[0].entities.push('parser')]){
  const candidate=structuredClone(f.model);change(candidate);assert.throws(()=>parseModel(JSON.stringify(candidate)),e=>e.code==='system/invalid');
 }
 const noTasks=structuredClone(legacy.model);noTasks.tasks=[];assert.equal(topologyHash(legacy.model),topologyHash(noTasks));
 const moved=structuredClone(f.model);moved.tasks[0].entities=['asr'];assert.notEqual(topologyHash(moved),topologyHash(f.model));
 moved.tasks[0].entities=['parser'];moved.tasks[0].status='done';assert.equal(topologyHash(moved),topologyHash(f.model));
});

test('tasks: Human selector, bounded Agent query, full reads and diffs have identical identities and associations',()=>{
 const f=setup(),s=snapshot(f),all=queryGraph(s,{mode:'board',detail:'full'});
 assert.deepEqual(all.records.map(r=>r.value),projectTasks(s.model).tasks);
 assert.deepEqual(queryGraph(s,{mode:'board',assignee:'alice'}).records.map(r=>r.value.id),['first']);
 assert.deepEqual(queryGraph(s,{mode:'board',target:'asr'}).records.map(r=>r.value.id),['second']);
 assert.equal(queryGraph(s,{mode:'board',status:'done'}).records.length,0);
 assert.equal(queryGraph(s,{mode:'board',search:'口径'}).records.length,2);
 assert.equal(queryGraph(s,{mode:'board',assignee:'__unassigned__'}).records.length,0);
 let page,records=[];do{const q=queryGraph(s,{mode:'board',detail:'full',limit:1,page});records.push(...q.records);page=q.page.next;}while(page);assert.deepEqual(records,all.records);
 const first=queryGraph(s,{mode:'board',limit:1});assert.throws(()=>queryGraph({...s,cursor:2},{mode:'board',limit:1,page:first.page.next}),e=>e.code==='query/page');
 assert.equal(queryGraph(s,{mode:'full'}).records.filter(r=>r.type==='task').length,2);
 assert.equal(queryGraph(s,{mode:'reach',target:'input'}).records.some(r=>r.type==='task'),false);
 const b=structuredClone(s);b.cursor=2;b.model.tasks[0].status='done';b.model.tasks.pop();b.model.tasks.push(task('new'));
 assert.deepEqual(diffSnapshots(s,b).records.filter(r=>r.collection==='tasks').map(r=>[r.id,r.operation]),[['first','updated'],['new','added'],['second','removed']]);
 // Serialized renderer must not accidentally capture Node imports.
 assert.doesNotThrow(()=>new Function('return '+installOtohaBoard.toString()));
});

test('tasks: API/CLI save is atomic, version checked, idempotent, durable, and preserves maturity',async()=>{
 const f=setup();let server=await startDesignPreview({...f.options,pollMs:60000});
 try{
  const session=await (await fetch(server.url+'api/session')).json();
  const post=async payload=>{const r=await fetch(server.url+'api/tasks',{method:'POST',headers:{Origin:server.url.slice(0,-1),'Content-Type':'application/json','X-Archify-Token':session.token},body:JSON.stringify(payload)});return {status:r.status,data:await r.json()};};
  const before=server.authority.snapshot(),payload={operationId:'move',expectedCursor:before.cursor,action:'update',taskId:'first',patch:{status:'done'}};
  assert.equal((await post(payload)).status,200);assert.equal((await post(payload)).data.replayed,true);assert.equal(server.authority.record().cursor,before.cursor+1);
  assert.deepEqual(server.authority.snapshot().model.entities,before.model.entities);
  assert.equal((await post({...payload,operationId:'stale',patch:{title:'lost update'}})).status,409);
  assert.equal((await post({...payload,patch:{status:'review'}})).status,409);
  const cursor=server.authority.record().cursor;
  assert.equal((await post({...payload,operationId:'bad',expectedCursor:cursor,patch:{entities:['missing']}})).status,400);
  assert.equal(server.authority.record().cursor,cursor);
  const cli=JSON.parse(execFileSync(process.execPath,['bin/otoha-graph.mjs','query',f.input,'--offline','--mode','board','--assignee','alice'],{encoding:'utf8'}));assert.equal(cli.records[0].value.status,'done');
  const views=server.authority.bundle().views;assert.deepEqual(views,server.authority.bundle(before.cursor).views);
  assert.equal(JSON.parse(fs.readFileSync(f.input)).tasks[0].status,'done');
  await server.stop();server=await startDesignPreview({...f.options,pollMs:60000});assert.equal(server.authority.snapshot().model.tasks[0].status,'done');
  fs.writeFileSync(f.input,'invalid source');server.authority.refresh();assert.ok(server.authority.failure);assert.equal(server.authority.query({mode:'board'}).records[0].value.status,'done');
  assert.throws(()=>mutate(server.authority,{...payload,operationId:'blocked',expectedCursor:cursor}),e=>e.code==='task/source-invalid');
 }finally{await server.stop();}
});

test('tasks: crash after accepted commit recovers mirror; unrelated local edits are preserved',()=>{
 const f=setup();let a=new GraphAuthority(f.options);a.acquire();a.refresh();
 try{
  const original=fs.readFileSync(f.input,'utf8'),beforeHash=digest(original);
  mutate(a,{operationId:'crash',expectedCursor:a.record().cursor,action:'update',taskId:'first',patch:{status:'review'}});
  const record=a.record(),source=a.loadObject(record.object).source,pending=path.join(a.directory,'pending-source.json');
  fs.writeFileSync(pending,JSON.stringify({transactionId:record.transactionId,beforeHash,source}));fs.writeFileSync(f.input,'unrelated edit');a.close();a=new GraphAuthority(f.options);a.acquire();a.refresh();
  assert.equal(a.failure.code,'authority/recovery-conflict');assert.equal(fs.readFileSync(f.input,'utf8'),'unrelated edit');assert.equal(a.snapshot().model.tasks[0].status,'review');
  fs.writeFileSync(f.input,original);a.refresh();assert.equal(a.failure,null);assert.equal(fs.readFileSync(f.input,'utf8'),source);assert.equal(a.records.length,2);assert.ok(!fs.existsSync(pending));
  assert.equal(mutate(a,{operationId:'crash',expectedCursor:1,action:'update',taskId:'first',patch:{status:'review'}}).replayed,true);
 }finally{a.close();}
});

test('tasks: optional links and scoped deletion/restore preserve the graph and intervening changes',()=>{
 const f=setup();let a=new GraphAuthority(f.options);a.acquire();a.refresh();
 try{
  const graph=structuredClone(a.snapshot().model.entities),relations=structuredClone(a.snapshot().model.relations);
  const command=(operationId,body)=>mutate(a,{operationId,expectedCursor:a.record().cursor,...body});
  command('unlink',{action:'update',taskId:'first',patch:{entities:[]}});
  assert.deepEqual(a.query({mode:'board',target:'first',detail:'full'}).records[0].value.entities,[]);
  const before=a.snapshot(),payload={operationId:'delete',expectedCursor:before.cursor,action:'delete',taskId:'first'};
  mutate(a,payload);assert.equal(mutate(a,payload).replayed,true);
  assert.equal(a.snapshot().model.tasks.some(t=>t.id==='first'),false);
  assert.deepEqual(diffSnapshots(before,a.snapshot()).records.filter(r=>r.collection==='tasks').map(r=>r.operation),['removed']);
  assert.throws(()=>mutate(a,{...payload,operationId:'stale'}),e=>e.code==='task/conflict');
  command('other-edit',{action:'update',taskId:'second',patch:{title:'Keep this newer edit'}});
  a.close();a=new GraphAuthority(f.options);a.acquire();a.refresh();
  command('restore',{action:'restore',deletionId:'delete'});
  assert.deepEqual(a.snapshot().model.tasks.find(t=>t.id==='first'),before.model.tasks.find(t=>t.id==='first'));
  assert.equal(a.snapshot().model.tasks.find(t=>t.id==='second').title,'Keep this newer edit');
  assert.deepEqual(a.snapshot().model.entities,graph);assert.deepEqual(a.snapshot().model.relations,relations);
  assert.throws(()=>command('restore-again',{action:'restore',deletionId:'delete'}),e=>e.code==='task/conflict');

 }finally{a.close();}
});

test('tasks: signed member fields, atomic rejection, stale fields, revocation and Git replicas',async()=>{
 const root=fs.mkdtempSync(path.join(os.tmpdir(),'otoha-task-team-')),f=setup(),remote=path.join(root,'remote.git');execFileSync('git',['init','--bare','--quiet',remote]);
 const directory=path.join(root,'leader'),memberDir=path.join(root,'alice');
 const invitation=initLeader({directory,input:f.input,repoRoot:f.root,projectId:'tasks',remote});
 const identity=initMember({directory:memberDir,actor:'alice',invitation}),leader=new TeamLeader(directory),member=new TeamMember(memberDir);
 try{
  leader.grant({...identity,grants:[],taskGrants:[{tasks:['first'],fields:['status','blocked']}]});member.accept(leader.publication());
  const edit=(id,field,value)=>({operation:'task.set',taskId:id,field,value,expectedVersion:member.snapshot().collaboration.fieldVersions['task:'+id+':'+field]});
  const accepted=member.prepare([edit('first','status','doing')]),stale=member.prepare([edit('first','status','done')]);
  assert.equal(leader.apply(accepted).status,'accepted');assert.equal(leader.apply(accepted).replayed,true);assert.equal(leader.apply(stale).code,'team/conflict');
  member.accept(leader.publication());
  const denied=leader.apply(member.prepare([edit('first','blocked','should not leak'),edit('second','status','done')]));assert.equal(denied.code,'team/forbidden');assert.equal(leader.authority.snapshot().model.tasks[0].blocked,'');
  assert.equal(leader.apply(member.prepare([edit('first','assignees',['leader'])])).code,'team/forbidden');
  const queued=member.prepare([edit('first','blocked','等待输入')]);leader.grant({...identity,grants:[],taskGrants:[]});assert.equal(leader.apply(queued).code,'team/forbidden');
  leader.grant({...identity,grants:[],taskGrants:[{tasks:['first'],fields:['blocked']}]});
  await leader.sync();await member.sync();const request=member.prepare([edit('first','blocked','等待原始数据')]);await member.sync();await leader.sync();await member.sync();
  assert.equal(member.snapshot().model.tasks[0].blocked,'等待原始数据');assert.deepEqual(member.query({mode:'board',detail:'full'}).records,leader.authority.query({mode:'board',detail:'full'}).records);
  const receipt=member.teamState().outbox.find(r=>r.requestId===request.payload.requestId).receipt;assert.equal(receipt.status,'accepted');assert.equal(receipt.changes[0].taskId,'first');
  member.accept(leader.publication());
  const beforeDelete=member.prepare([edit('first','blocked','stale after restore')]);
  const authorityChange=(operationId,body)=>changeTask(leader.authority,{operationId,expectedCursor:leader.authority.record().cursor,...body},(source,reason,extra)=>leader.transact(source,leader.authority.snapshot().collaboration,reason,extra));
  authorityChange('leader-delete',{action:'delete',taskId:'first'});
  assert.equal(leader.authority.snapshot().model.tasks.some(t=>t.id==='first'),false);
  authorityChange('leader-restore',{action:'restore',deletionId:'leader-delete'});
  assert.equal(leader.apply(beforeDelete).code,'team/conflict');
  member.accept(leader.publication());assert.deepEqual(member.query({mode:'board',detail:'full'}).records,leader.authority.query({mode:'board',detail:'full'}).records);
  const preview=await startDesignPreview({input:member.options.input,team:member,pollMs:60000});
  try{const session=await(await fetch(preview.url+'api/session')).json();
   for(const command of [{action:'create',task:task('elevate')},{action:'delete',taskId:'first'},{action:'restore',deletionId:'unknown'}]){
    const r=await fetch(preview.url+'api/tasks',{method:'POST',headers:{Origin:preview.url.slice(0,-1),'Content-Type':'application/json','X-Archify-Token':session.token},body:JSON.stringify({...command,operationId:'unauthorized-'+command.action,expectedCursor:member.snapshot().cursor})});assert.equal(r.status,403);
   }
  }finally{await preview.stop();}

 }finally{leader.close();member.close();}
});

test('filters: bounded shared presets preserve authority, boundaries, pagination and explicit target scope',()=>{
 const f=setup();f.model.tasks[0].blocked='waiting';f.model.tasks[0].entities=['decode'];f.model.tasks[1].assignees=[];
 const s=snapshot(f),before=JSON.stringify(s);
 for(const [filter,ids] of [['all',['first','second']],['active',['first','second']],['blocked',['first']],['review',[]],['unassigned',['second']]])assert.deepEqual(queryGraph(s,{mode:'board',filter}).records.map(r=>r.value.id),ids);
 assert.equal(queryGraph(s,{mode:'board',filter:'blocked',assignee:'bob'}).records.length,0);
 const page=queryGraph(s,{mode:'board',filter:'active',limit:1});assert.equal(page.page.hasMore,true);
 assert.throws(()=>queryGraph(s,{mode:'board',filter:'all',limit:1,page:page.page.next}),e=>e.code==='query/page');
 const q=queryGraph(s,{mode:'view',view:'overview',expanded:'parser,parser/asr',filter:'blocked'});
 assert.deepEqual(q.records.filter(r=>r.type==='entity').map(r=>r.value.id),['asr','decode','parser']);assert.equal(q.filter.context,2);assert.equal(q.filter.matched,1);
 assert.ok(q.records.some(r=>r.type==='boundary'&&r.value.id==='decode-emit'));
 const ids=filter=>queryGraph(s,{mode:'view',view:'overview',filter,target:'parser'}).records.filter(r=>r.type==='entity').map(r=>r.value.id);
 assert.deepEqual(ids('upstream'),['input','parser']);assert.deepEqual(ids('downstream'),['parser','session']);assert.deepEqual(ids('neighbors'),['input','parser','session']);
 assert.throws(()=>queryGraph(s,{mode:'view',view:'overview',filter:'neighbors'}),e=>e.code==='query/argument');
 for(const query of [{mode:'board',filter:''},{mode:'board',filter:'typo'},{mode:'view',view:'overview',filter:'active'},{mode:'full',filter:'blocked'}])assert.throws(()=>queryGraph(s,query),e=>e.code==='query/argument');
 assert.equal(JSON.stringify(s),before);
});
