import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { execFileSync, execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { fixture } from './helpers/system-fixture.mjs';
import { initLeader, TeamLeader } from '../team/leader.mjs';
import { initMember, TeamMember } from '../team/member.mjs';
import { canonical, signed, readJSON } from '../team/protocol.mjs';
import { startDesignPreview } from '../design/server.mjs';

function setup(){
  const root=fs.mkdtempSync(path.join(os.tmpdir(),'otoha-team-')),f=fixture(path.join(root,'project')),remote=path.join(root,'mailbox.git');
  execFileSync('git',['init','--bare','--quiet',remote]);
  const leaderDir=path.join(root,'leader'),memberDir=path.join(root,'alice'),bobDir=path.join(root,'bob');
  const invitation=initLeader({directory:leaderDir,input:f.input,repoRoot:f.root,projectId:'test-project',remote});
  const alice=initMember({directory:memberDir,actor:'alice',invitation}),bob=initMember({directory:bobDir,actor:'bob',invitation});
  const leader=new TeamLeader(leaderDir);
  leader.grant({...alice,grants:[{nodes:['parser'],fields:['inputs','outputs'],comments:true}],agentId:'codex',sessionId:'session-a'});
  leader.grant({...bob,grants:[{nodes:['parser'],fields:[],comments:true}]});
  const member=new TeamMember(memberDir);member.accept(leader.publication());
  const other=new TeamMember(bobDir);other.accept(leader.publication());
  return {root,f,remote,leaderDir,memberDir,bobDir,leader,member,other,alice,bob,close:()=>{leader.close();member.close();other.close();}};
}
const change=(value,nodeId='parser',field='inputs')=>({operation:'field.set',nodeId,field,value});
const node=f=>f.leader.authority.snapshot().model.entities.find(e=>e.id==='parser');

test('team: malformed local outbox entries are retained and isolated from valid sync and HTTP reads',async()=>{
  const f=setup();let server;
  try{
    await f.leader.sync();
    const good=f.member.prepare([change(['surviving request'])],{},'valid-neighbor');
    const outbox=path.join(f.memberDir,'outbox'),bad=new Map();
    const put=(name,bytes)=>{bad.set(name,bytes);fs.writeFileSync(path.join(outbox,name),bytes);};
    put('broken.json','{unfinished');put('shape.json',JSON.stringify({payload:null}));
    put('signature.json',canonical({...good,signature:'invalid'}));
    put('oversize.json','x'.repeat(70001));
    put('request-shape.json',canonical(signed({...good.payload,requestId:'request-shape',changes:[]},f.member.key)));
    put('wrong-actor.json',canonical(signed({...good.payload,requestId:'wrong-actor',actor:'bob'},f.member.key)));
    put('wrong-epoch.json',canonical(signed({...good.payload,requestId:'wrong-epoch',epoch:'foreign'},f.member.key)));
    put('wrong-project.json',canonical(signed({...good.payload,requestId:'wrong-project',projectId:'foreign'},f.member.key)));
    put('wrong-filename.json',canonical(good));put('bad name.json',canonical(good));
    assert.throws(()=>f.member.prepare([change(['do not overwrite'])],{},'broken'));
    const before=f.member.teamState();assert.equal(before.outbox.length,1);assert.equal(before.outboxErrors.length,bad.size);
    const result=await f.member.sync();assert.equal(result.ok,false);assert.equal(result.partial,true);assert.equal(result.submitted,1);assert.equal(result.outboxErrors.length,bad.size);
    const head=await f.member.transport.fetch(),remotePath='otoha/test-project/requests/alice/';
    assert.ok(await f.member.transport.read(head,remotePath+'valid-neighbor.json',70000));
    for(const name of bad.keys())assert.equal(await f.member.transport.read(head,remotePath+name,70000),null);
    await f.leader.sync();await f.member.sync();
    assert.equal(f.member.teamState().outbox[0].receipt.status,'accepted');assert.deepEqual(node(f).inputs,['surviving request']);
    for(const [name,bytes] of bad)assert.equal(fs.readFileSync(path.join(outbox,name),'utf8'),bytes);
    server=await startDesignPreview({input:f.member.options.input,team:f.member});
    const response=await fetch(server.url+'api/team');assert.equal(response.status,200);
    const state=await response.json();assert.equal(state.outboxErrors.length,bad.size);assert.equal(state.outbox[0].receipt.status,'accepted');
    const q=await fetch(server.url+'api/query?mode=overview');assert.equal(q.status,200);
    // Explicit local repair resumes that ID; other bad entries remain isolated.
    const repaired=signed({...good.payload,requestId:'broken',changes:[{operation:'comment.add',nodeId:'parser',text:'Repaired local request'}]},f.member.key);
    fs.writeFileSync(path.join(outbox,'broken.json'),canonical(repaired));
    assert.equal((await f.member.sync()).submitted,1);await f.leader.sync();await f.member.sync();
    assert.equal(f.member.teamState().outbox.find(r=>r.requestId==='broken').receipt.status,'accepted');
    assert.equal(f.member.teamState().outboxErrors.length,bad.size-1);
    for(const name of bad.keys())if(name!=='broken.json')fs.unlinkSync(path.join(outbox,name));
    const recovered=await f.member.sync();assert.equal(recovered.ok,true);assert.deepEqual(recovered.outboxErrors,[]);
  }finally{await server?.stop();f.close();fs.rmSync(f.root,{recursive:true,force:true});}
});

test('team: damaged durable leader history cannot sign or republish an older verified prefix',async()=>{
  const f=setup();let damaged,repaired;
  try{
    f.leader.apply(f.member.prepare([change(['latest published'])]));await f.leader.sync();
    const cursor=f.leader.authority.record().cursor;
    const commit=path.join(f.leader.authority.directory,'commits',String(cursor).padStart(12,'0')+'.json');
    const original=fs.readFileSync(commit);f.leader.close();fs.writeFileSync(commit,'{corrupt');
    damaged=new TeamLeader(f.leaderDir);assert.ok(damaged.authority.storeFailure);
    assert.equal(damaged.authority.record().cursor,cursor-1);
    assert.throws(()=>damaged.publication(),e=>e.code==='authority/store-damaged');
    await assert.rejects(damaged.sync(),e=>e.code==='authority/store-damaged');
    const head=await f.member.transport.fetch();
    const remote=JSON.parse(await f.member.transport.read(head,'otoha/test-project/snapshot.json',32*1024*1024));
    assert.equal(remote.payload.cursor,cursor);assert.equal(fs.readFileSync(commit,'utf8'),'{corrupt');
    damaged.close();damaged=null;fs.writeFileSync(commit,original);
    repaired=new TeamLeader(f.leaderDir);assert.equal(repaired.authority.storeFailure,null);
    assert.equal(repaired.authority.record().cursor,cursor);
    assert.equal((await repaired.sync()).cursor,cursor);
  }finally{damaged?.close();repaired?.close();f.close();fs.rmSync(f.root,{recursive:true,force:true});}
});

test('team: private authority cannot be initialized inside a Git worktree',()=>{
  const root=fs.mkdtempSync(path.join(os.tmpdir(),'otoha-team-location-'));execFileSync('git',['init','--quiet',root]);const f=fixture(root);
  assert.throws(()=>initLeader({directory:path.join(root,'state'),input:f.input,projectId:'project',remote:root}),e=>e.code==='team/state-location');
  assert.equal(fs.existsSync(path.join(root,'state','private.pem')),false);
});

test('team: authorized node edits and comments share one graph and actor/session attribution',()=>{
  const f=setup();try{
    const result=f.leader.apply(f.member.prepare([change(['PCM']),{operation:'comment.add',nodeId:'parser',text:'IO definition supplied'}],{agentId:'codex',sessionId:'session-a'}));
    assert.equal(result.status,'accepted');assert.deepEqual(node(f).inputs,['PCM']);
    f.member.accept(f.leader.publication());
    assert.deepEqual(f.member.query({mode:'full',detail:'full'}).records,f.leader.authority.query({mode:'full',detail:'full'}).records);
    assert.equal(f.member.snapshot().collaboration.comments[0].context.sessionId,'session-a');
  }finally{f.close();}
});

test('team: node, field and initialize-only grants reject unauthorized requests',()=>{
  const f=setup();try{
    assert.equal(f.leader.apply(f.member.prepare([change(['bad'],'session')])).code,'team/forbidden');
    assert.equal(f.leader.apply(f.member.prepare([change('Bad label','parser','label')])).code,'team/forbidden');
    assert.equal(f.leader.apply(f.other.prepare([change(['bad'])])).code,'team/forbidden');
    f.leader.grant({...f.alice,grants:[{nodes:['parser'],fields:['inputs'],onlyIfEmpty:true}]});
    assert.equal(f.leader.apply(f.member.prepare([change(['bad'])])).code,'team/forbidden');
    assert.deepEqual(node(f).inputs,['input']);
  }finally{f.close();}
});

test('team: forged actor, self-elevation, topology and schema escape cannot gain permission',()=>{
  const f=setup();try{
    const envelope=f.member.prepare([change(['PCM'])]);envelope.payload.actor='bob';
    assert.throws(()=>f.leader.apply(envelope),e=>e.code==='team/signature');
    const raw=f.member.prepare([change(['PCM'])]).payload;
    assert.throws(()=>f.leader.apply(signed({...raw,role:'leader'},f.member.key)),e=>e.code==='team/shape');
    assert.throws(()=>f.leader.apply(signed({...raw,changes:[{operation:'node.delete',nodeId:'parser'}]},f.member.key)),e=>e.code==='team/operation');
    assert.throws(()=>f.leader.apply(signed({...raw,changes:[{...raw.changes[0],field:'__proto__'}]},f.member.key)),e=>e.code==='team/operation');
    assert.deepEqual(node(f).inputs,['input']);
  }finally{f.close();}
});

test('team: stale unrelated fields still apply, overlapping edits and ABA conflict',()=>{
  const f=setup();try{
    const a=f.member.prepare([change(['A'])]),b=f.member.prepare([change(['B'],'parser','outputs')]),stale=f.member.prepare([change(['old'])]);
    assert.equal(f.leader.apply(a).status,'accepted');assert.equal(f.leader.apply(b).status,'accepted');
    assert.equal(f.leader.apply(stale).code,'team/conflict');
    const source=readJSON(f.leader.input);source.entities.find(e=>e.id==='parser').inputs=['input'];fs.writeFileSync(f.leader.input,JSON.stringify(source));f.leader.authority.refresh();
    assert.equal(f.leader.apply(f.member.prepare([change(['still stale'])])).code,'team/conflict');
  }finally{f.close();}
});

test('team: atomic batch rejection, duplicate retry and ID reuse retain correct state',()=>{
  const f=setup();try{
    const denied=f.leader.apply(f.member.prepare([{operation:'comment.add',nodeId:'parser',text:'must not leak'},change(['bad'],'session')]));
    assert.equal(denied.status,'rejected');assert.equal(f.leader.authority.snapshot().collaboration.comments.length,0);
    const req=f.member.prepare([change(['PCM'])]);const accepted=f.leader.apply(req),before=f.leader.authority.current.cursor;
    assert.equal(f.leader.apply(req).replayed,true);assert.equal(f.leader.authority.current.cursor,before);
    const reused=structuredClone(req.payload);reused.changes[0].value=['Different'];assert.throws(()=>f.leader.apply(signed(reused,f.member.key)),e=>e.code==='team/idempotency-conflict');
    assert.equal(accepted.status,'accepted');
  }finally{f.close();}
});

test('team: revocation is checked at acceptance, not when member created request',()=>{
  const f=setup();try{const req=f.member.prepare([change(['PCM'])]);f.leader.grant({...f.alice,grants:[]});assert.equal(f.leader.apply(req).code,'team/forbidden');}finally{f.close();}
});

test('team: commit-before-mirror crash is recovered without applying a request twice',()=>{
  const f=setup();let leader=f.leader;
  try{
    const original=fs.readFileSync(leader.input,'utf8'),beforeHash=leader.authority.snapshot().revision,req=f.member.prepare([change(['PCM'])]);leader.apply(req);
    const record=leader.authority.record(),source=leader.authority.loadObject(record.object).source;
    fs.writeFileSync(path.join(f.leaderDir,'pending-source.json'),JSON.stringify({transactionId:record.transactionId,beforeHash,source}));fs.writeFileSync(leader.input,original);leader.close();
    leader=new TeamLeader(f.leaderDir);assert.deepEqual(leader.authority.snapshot().model.entities.find(e=>e.id==='parser').inputs,['PCM']);assert.equal(leader.apply(req).replayed,true);
    assert.equal(fs.existsSync(path.join(f.leaderDir,'pending-source.json')),false);
  }finally{leader.close();f.member.close();f.other.close();}
});

test('team: real Git roundtrip accepts requests, ignores remote graph/policy changes and repairs publication',async()=>{
  const f=setup();try{
    await f.leader.sync();await f.member.sync();const request=f.member.prepare([change(['PCM from teammate'])]);await f.member.sync();await f.leader.sync();await f.member.sync();
    assert.equal(f.member.teamState().outbox.find(r=>r.requestId===request.payload.requestId).receipt.status,'accepted');
    const good=canonical(f.leader.publication()),bad=JSON.parse(good);bad.payload.snapshot.model.entities[0].label='Hijacked';bad.payload.snapshot.collaboration.members.push({actor:'mallory',publicKey:f.bob.publicKey,grants:[{nodes:['parser'],fields:['label']}]});
    await f.member.transport.publish({'otoha/test-project/snapshot.json':canonical(bad)},'Unauthorized published graph edit');
    await assert.rejects(f.member.sync(),e=>e.code==='team/signature');assert.notEqual(f.member.snapshot().model.entities[0].label,'Hijacked');
    await f.leader.sync();await f.member.sync();const head=await f.member.transport.fetch();assert.equal(await f.member.transport.read(head,'otoha/test-project/snapshot.json',32*1024*1024),good);
    assert.ok(fs.readdirSync(path.join(f.leaderDir,'remote-observations')).length);
    assert.deepEqual(node(f).inputs,['PCM from teammate']);assert.equal(f.leader.authority.snapshot().collaboration.members.some(m=>m.actor==='mallory'),false);
  }finally{f.close();}
});

test('team: invalid signatures, foreign epochs and old signed snapshots never replace verified replica',()=>{
  const f=setup();try{
    const old=f.leader.publication();f.leader.apply(f.member.prepare([change(['new'])]));f.member.accept(f.leader.publication());
    assert.throws(()=>f.member.accept(old),e=>e.code==='team/replay');
    const foreign=structuredClone(old.payload);foreign.epoch='foreign';assert.throws(()=>f.member.accept(signed(foreign,f.leader.key)),e=>e.code==='team/publication');
    f.member.close();const restarted=new TeamMember(f.memberDir);try{assert.equal(restarted.snapshot().cursor,f.member.snapshot().cursor);}finally{restarted.close();}
  }finally{f.close();}
});

test('team: HTTP Human and Agent share signed snapshot, member cannot bypass through legacy mutation routes',async()=>{
  const f=setup();const server=await startDesignPreview({input:f.member.options.input,team:f.member});
  try{
    const session=await (await fetch(server.url+'api/session')).json();
    const query=await (await fetch(server.url+'api/query?mode=full&detail=full')).json();assert.deepEqual(query.records,f.leader.authority.query({mode:'full',detail:'full'}).records);
    for(const route of ['api/team/grant','api/rollback','api/requests','api/agent-request']){
      const r=await fetch(server.url+route,{method:'POST',headers:{Origin:new URL(server.url).origin,'Content-Type':'application/json','X-Archify-Token':session.token},body:'{}'});assert.equal(r.status,403,route);
    }
    const r=await fetch(server.url+'api/team/request',{method:'POST',headers:{Origin:new URL(server.url).origin,'Content-Type':'application/json','X-Archify-Token':session.token},body:JSON.stringify({changes:[change(['UI request'])]})});assert.equal(r.status,200);assert.equal(f.member.teamState().outbox.length,1);
  }finally{await server.stop();f.close();}
});

test('team: member writer lock, CLI forwarding and retry keep one signed request identity',async()=>{
  const f=setup(),run=promisify(execFile),cli=path.resolve('bin/otoha-graph.mjs');
  const memberServer=await startDesignPreview({input:f.member.options.input,team:f.member});
  const leaderServer=await startDesignPreview({input:f.leader.input,repoRoot:f.f.root,team:f.leader});
  const call=async(args)=>JSON.parse((await run(process.execPath,[cli,'team',...args])).stdout);
  try{
    assert.throws(()=>new TeamMember(f.memberDir),e=>e.code==='authority/locked');
    const payload=path.join(f.root,'request.json');fs.writeFileSync(payload,JSON.stringify({requestId:'retry-safe',changes:[change(['CLI input'])]}));
    const submitted=await call(['request','--state',f.memberDir,'--payload',payload]);assert.equal(submitted.request.requestId,'retry-safe');
    await call(['sync','--state',f.leaderDir]);await call(['sync','--state',f.memberDir]);await call(['sync','--state',f.leaderDir]);await call(['sync','--state',f.memberDir]);
    const retry=await call(['request','--state',f.memberDir,'--payload',payload]);assert.deepEqual(retry.request,submitted.request);
    const view=await call(['query','--state',f.memberDir,'--mode','full','--detail','full']);assert.deepEqual(view.records,f.leader.authority.query({mode:'full',detail:'full'}).records);
    const policy=path.join(f.root,'grant.json');fs.writeFileSync(policy,JSON.stringify({...f.alice,grants:[]}));
    const granted=await call(['grant','--state',f.leaderDir,'--payload',policy]);assert.equal(granted.ok,true);
    await assert.rejects(call(['grant','--state',f.memberDir,'--payload',policy]),e=>JSON.parse(e.stderr).code==='team/forbidden');
  }finally{await memberServer.stop();await leaderServer.stop();f.close();}
});

test('team: malformed field value, revoked remote request and forged mailbox data do not mutate authority',async()=>{
  const f=setup();try{
    const invalid=f.leader.apply(f.member.prepare([change('not-an-array')]));assert.equal(invalid.status,'rejected');
    const forged=f.other.prepare([change(['forged'])]);forged.payload.actor='alice';
    const wrongNode=f.member.prepare([change(['forbidden'],'session')]);
    await f.member.transport.publish({
      ['otoha/test-project/requests/alice/'+forged.payload.requestId+'.json']:canonical(forged),
      ['otoha/test-project/requests/alice/'+wrongNode.payload.requestId+'.json']:canonical(wrongNode)
    },'Simulate teammate ignoring the protocol');
    const result=await f.leader.sync();assert.ok(result.outcomes.some(r=>r.code==='team/signature'));assert.ok(result.outcomes.some(r=>r.code==='team/forbidden'));
    assert.equal(fs.readdirSync(path.join(f.leaderDir,'quarantine')).length,1);assert.deepEqual(node(f).inputs,['input']);
  }finally{f.close();}
});

test('team: disjoint concurrent Git submissions survive publication races and network outages',async()=>{
  const f=setup();try{
    await f.leader.sync();await Promise.all([f.member.sync(),f.other.sync()]);
    f.member.prepare([change(['concurrent'])]);f.other.prepare([{operation:'comment.add',nodeId:'parser',text:'parallel comment'}]);
    await Promise.all([f.member.sync(),f.other.sync()]);await f.leader.sync();await f.member.sync();
    assert.equal(f.member.snapshot().collaboration.receipts.filter(r=>r.status==='accepted').length,2);
    const cursor=f.member.current.cursor,fetch=f.member.transport.fetch.bind(f.member.transport);
    f.member.transport.fetch=async()=>{throw Error('offline test');};await assert.rejects(f.member.sync(),/offline test/);assert.equal(f.member.current.cursor,cursor);
    f.member.transport.fetch=fetch;await f.member.sync();assert.equal(f.member.status().failure,null);
  }finally{f.close();}
});

test('team: damaged replica falls back, writer recovers, and unrefreshed leader edits cannot be overwritten',()=>{
  const f=setup();try{
    const oldCursor=f.member.current.cursor;f.leader.apply(f.member.prepare([change(['latest'])]));f.member.accept(f.leader.publication());
    const latest=f.member.current.cursor;f.member.close();fs.writeFileSync(path.join(f.memberDir,'replica',String(latest).padStart(12,'0')+'.json'),'broken');
    const recovered=new TeamMember(f.memberDir);try{assert.equal(recovered.current.cursor,oldCursor);assert.equal(recovered.failure.code,'team/cache-damaged');recovered.accept(f.leader.publication());assert.equal(recovered.current.cursor,latest);}finally{recovered.close();}
    const c=f.leader.authority.snapshot().collaboration,oldSource=f.leader.authority.loadObject(f.leader.authority.record().object).source;
    const local=JSON.parse(oldSource);local.entities.find(e=>e.id==='parser').outputs=['unrefreshed local change'];fs.writeFileSync(f.leader.input,JSON.stringify(local));
    assert.throws(()=>f.leader.transact(oldSource,c,'test-race'),e=>e.code==='team/conflict');assert.deepEqual(readJSON(f.leader.input).entities.find(e=>e.id==='parser').outputs,['unrefreshed local change']);
    f.leader.authority.refresh();
    const commit=f.leader.authority.commit.bind(f.leader.authority);
    f.leader.authority.commit=(...args)=>{const result=commit(...args);local.entities.find(e=>e.id==='parser').inputs=['concurrent editor write after commit'];fs.writeFileSync(f.leader.input,JSON.stringify(local));return result;};
    assert.throws(()=>f.leader.grant({...f.alice,grants:[]}),e=>e.code==='team/recovery-conflict');
    assert.deepEqual(readJSON(f.leader.input).entities.find(e=>e.id==='parser').inputs,['concurrent editor write after commit']);assert.ok(fs.existsSync(f.leader.pending));
  }finally{f.close();}
});

test('team: leader can add/remove topology locally; invalid topology preserves the accepted graph',()=>{
  const f=setup();try{
    const model=readJSON(f.leader.input),extra={...structuredClone(model.entities[0]),id:'monitor',label:'monitor'};
    model.entities.push(extra);model.relations.push({id:'session-monitor',from:'session',to:'monitor',kind:'dataflow',label:'status'});
    model.views[0].placements.push({entity:'monitor',pos:[1030,180]});model.views[0].relations.push({relation:'session-monitor'});
    fs.writeFileSync(f.leader.input,JSON.stringify(model));assert.equal(f.leader.authority.refresh(),true);assert.equal(f.leader.authority.failure,null);
    f.member.accept(f.leader.publication());assert.equal(f.member.snapshot().model.entities.length,8);
    const cursor=f.leader.authority.current.cursor;model.relations.at(-1).to='missing-node';fs.writeFileSync(f.leader.input,JSON.stringify(model));f.leader.authority.refresh();
    assert.ok(f.leader.authority.failure);assert.equal(f.leader.authority.current.cursor,cursor);
    model.entities.pop();model.relations.pop();model.views[0].placements.pop();model.views[0].relations.pop();fs.writeFileSync(f.leader.input,JSON.stringify(model));f.leader.authority.refresh();
    assert.equal(f.leader.authority.failure,null);f.member.accept(f.leader.publication());assert.equal(f.member.snapshot().model.entities.length,7);
    assert.deepEqual(f.member.query({mode:'full',detail:'full'}).records,f.leader.authority.query({mode:'full',detail:'full'}).records);
  }finally{f.close();}
});
