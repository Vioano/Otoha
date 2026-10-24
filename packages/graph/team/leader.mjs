import fs from 'node:fs';
import path from 'node:path';
import { randomUUID } from 'node:crypto';
import { GraphAuthority, atomicWrite, sourceFingerprint } from '../design/authority.mjs';
import { parseModel, problem } from '../design/model.mjs';
import { canonical, hash, id, initializeIdentity, readJSON, signed, verified, validateGrant, validateRequest } from './protocol.mjs';
import { GitTransport } from './transport.mjs';

export function initLeader(options){
  if(!id(options.projectId))problem('team/project','Invalid project ID');
  const source=fs.readFileSync(options.input);parseModel(source);
  const directory=path.resolve(options.directory),identity=initializeIdentity(directory,options.actor||'leader');
  const config={version:1,role:'leader',projectId:options.projectId,epoch:randomUUID(),actor:identity.actor,leaderKey:identity.publicKey,remote:options.remote,branch:options.branch||'otoha-sync',repoRoot:options.repoRoot?path.resolve(options.repoRoot):null};
  atomicWrite(path.join(directory,'config.json'),JSON.stringify(config));atomicWrite(path.join(directory,'model.json'),source);
  const leader=new TeamLeader(directory);
  try{return leader.invite();}finally{leader.close();}
}
export class TeamLeader {
  constructor(directory){
    this.directory=path.resolve(directory);this.config=readJSON(path.join(directory,'config.json'));
    if(this.config.role!=='leader')problem('team/role','Leader operation requires the private leader installation',{},403);
    this.key=fs.readFileSync(path.join(directory,'private.pem'),'utf8');
    this.input=path.join(this.directory,'model.json');this.pending=path.join(directory,'pending-source.json');
    this.authority=new GraphAuthority({input:this.input,stateDir:path.join(directory,'graph'),repoRoot:this.config.repoRoot});this.authority.acquire();
    try{
      this.recover();this.authority.refresh({forceEvidence:true});this.authority.record();
      if(!this.authority.snapshot().collaboration){
        const r=this.authority.record(),object=this.authority.loadObject(r.object);
        this.authority.commit({...object,collaboration:{version:1,projectId:this.config.projectId,epoch:this.config.epoch,leaderKey:this.config.leaderKey,members:[],comments:[],receipts:[],fieldVersions:{}}},'team-initial');
      }
    }catch(error){this.close();throw error;}
    this.transport=new GitTransport(directory,this.config.remote,this.config.branch);this.syncFailure=null;
  }
  close(){this.authority.close();}
  invite(){const {projectId,epoch,leaderKey,remote,branch}=this.config;return {version:1,projectId,epoch,leaderKey,remote,branch};}
  recover(){
    if(!fs.existsSync(this.pending))return;
    const p=readJSON(this.pending),committed=this.authority.records.find(r=>r.transactionId===p.transactionId);
    if(committed){
      if(![p.beforeHash,parseModel(p.source).revision].includes(sourceFingerprint(this.input)))problem('team/recovery-conflict','Unrelated local edit found during crash recovery; preserve it before repair',{},409);
      atomicWrite(this.input,p.source);
    }
    fs.unlinkSync(this.pending);
  }
  transact(source,collaboration,reason,extra={}){
    this.recover();this.authority.writable();
    if(this.authority.failure)problem('team/source-invalid','Fix or rollback the leader source before processing requests',this.authority.failure,409);
    const loaded=parseModel(source),beforeHash=sourceFingerprint(this.input),transactionId=randomUUID();
    const old=this.authority.loadObject(this.authority.record().object);
    if(beforeHash!==old.snapshot.revision)problem('team/conflict','Leader source changed before this transaction; refresh before retrying',{},409);
    const built=loaded.revision===old.snapshot.revision?old:this.authority.prepare(loaded.bytes);
    if(beforeHash!==sourceFingerprint(this.input))problem('team/conflict','Leader source changed while preparing transaction',{},409);
    atomicWrite(this.pending,JSON.stringify({transactionId,beforeHash,source:loaded.bytes.toString()}));
    const record=this.authority.commit({...old,source:loaded.bytes.toString(),snapshot:built.snapshot,views:built.views,collaboration},reason,{transactionId,...extra});
    // The commit is the transaction boundary. Restart finishes a missing mirror write.
    if(![beforeHash,loaded.revision].includes(sourceFingerprint(this.input)))problem('team/recovery-conflict','Local source changed after commit; preserve it before completing the pending mirror',{},409);
    atomicWrite(this.input,loaded.bytes);fs.unlinkSync(this.pending);return record;
  }
  grant(value){
    validateGrant(value);this.authority.refresh();
    const snapshot=this.authority.snapshot(),c=structuredClone(snapshot.collaboration);
    for(const g of value.grants)for(const node of g.nodes)if(!snapshot.model.entities.some(e=>e.id===node))problem('team/policy','Grant refers to unknown node',{node});
    for(const g of value.taskGrants||[])for(const taskId of g.tasks)if(!snapshot.model.tasks?.some(t=>t.id===taskId))problem('team/policy','Grant refers to unknown task',{taskId});
    c.members=c.members.filter(m=>m.actor!==value.actor);c.members.push(value);c.members.sort((a,b)=>a.actor.localeCompare(b.actor));
    return this.transact(this.authority.loadObject(this.authority.record().object).source,c,'team-policy');
  }
  apply(envelope){
    this.recover();this.authority.refresh();
    const snapshot=this.authority.snapshot(),c=structuredClone(snapshot.collaboration),claimed=envelope?.payload;
    const member=c.members.find(m=>m.actor===claimed?.actor);
    if(!member)problem('team/identity','Unknown or revoked member',{},403);
    const request=validateRequest(verified(envelope,member.publicKey));
    if(request.projectId!==c.projectId||request.epoch!==c.epoch)problem('team/project','Request targets another project or authority epoch',{},403);
    const payloadHash=hash(request),prior=c.receipts.find(r=>r.actor===request.actor&&r.requestId===request.requestId);
    if(prior){if(prior.payloadHash!==payloadHash)problem('team/idempotency-conflict','Request ID was reused with different content',{},409);return {...prior,replayed:true};}
    const loaded=parseModel(this.authority.loadObject(this.authority.record().object).source),candidate=structuredClone(loaded.model);
    let status='accepted',code=null,message='Applied atomically';
    try{
      if(request.baseCursor>snapshot.cursor)problem('team/conflict','Request refers to an unknown future version',{},409);
      for(const [index,change] of request.changes.entries()){
        if(change.operation==='task.set'){
          const task=candidate.tasks?.find(t=>t.id===change.taskId);if(!task)problem('team/task-missing','Task no longer exists',{},409);
          if(!member.taskGrants?.some(g=>g.tasks.includes(change.taskId)&&g.fields.includes(change.field)))problem('team/forbidden','Task field permission not granted',{},403);
          if(c.fieldVersions['task:'+change.taskId+':'+change.field]!==change.expectedVersion)problem('team/conflict','Task field changed; reread before resubmitting',{},409);
          task[change.field]=change.value;continue;
        }
        const node=candidate.entities.find(e=>e.id===change.nodeId);if(!node)problem('team/node-missing','Target node no longer exists',{},409);
        const allowed=member.grants.filter(g=>g.nodes.includes(change.nodeId));
        if(change.operation==='comment.add'){
          if(!allowed.some(g=>g.comments))problem('team/forbidden','Comment permission not granted',{},403);
          c.comments.push({id:request.actor+'/'+request.requestId+'/'+index,nodeId:change.nodeId,actor:request.actor,text:change.text,context:request.context||{},at:new Date().toISOString()});
        }else{
          const grant=allowed.find(g=>g.fields.includes(change.field)&&(!g.onlyIfEmpty||(Array.isArray(node[change.field])?node[change.field].length===0:node[change.field]==='')));
          if(!grant)problem('team/forbidden','Field or overwrite permission not granted',{},403);
          if(c.fieldVersions[change.nodeId+':'+change.field]!==change.expectedVersion)problem('team/conflict','Target field has changed; read current data before resubmitting',{},409);
          node[change.field]=change.value;
        }
      }
      parseModel(JSON.stringify(candidate));
      // Includes graph projection/layout validation before anything is committed.
      if(hash(candidate)!==hash(loaded.model))this.authority.prepare(JSON.stringify(candidate));
    }catch(error){status='rejected';code=error.code||'team/validation';message=error.message;c.comments=snapshot.collaboration.comments;}
    const receipt={requestId:request.requestId,actor:request.actor,payloadHash,status,code,message,changes:request.changes.map(({nodeId,taskId,operation,field})=>({...(taskId?{taskId}:{nodeId}),operation,...(field?{field}:{})})),context:request.context||{},cursor:snapshot.cursor+1,at:new Date().toISOString()};
    c.receipts.push(receipt);
    this.transact(status==='accepted'?JSON.stringify(candidate):loaded.bytes,c,'team-request',{teamRequest:request.actor+'/'+request.requestId});
    return receipt;
  }
  publication(){
    // A verified prefix remains readable, but must never be signed as the new
    // authoritative tip while later durable history is damaged.
    this.authority.writable();
    const snapshot=this.authority.snapshot(),object=this.authority.loadObject(this.authority.record().object);
    return signed({version:1,projectId:this.config.projectId,epoch:this.config.epoch,cursor:snapshot.cursor,source:object.source,snapshot},this.key);
  }
  async sync(){
    if(this.syncing)return this.syncing;
    this.syncing=this.syncOnce().finally(()=>{this.syncing=null;});return this.syncing;
  }
  async syncOnce(){
    try{
      this.authority.writable();
      this.recover();this.authority.refresh();const head=await this.transport.fetch(),prefix='otoha/'+this.config.projectId+'/',outcomes=[];
      const remote=await this.transport.read(head,prefix+'snapshot.json',32*1024*1024).catch(e=>'Unreadable remote snapshot: '+e.message);
      // Preserve unexpected published bytes for inspection. Never import them.
      const publication=canonical(this.publication());
      if(remote&&remote!==publication)atomicWrite(path.join(this.directory,'remote-observations',hash(remote)+'.txt'),remote);
      for(const entry of await this.transport.list(head,prefix+'requests/')){
        try{
          const text=await this.transport.read(head,entry.name,70000),envelope=JSON.parse(text);
          const expected=prefix+'requests/'+envelope.payload?.actor+'/'+envelope.payload?.requestId+'.json';
          if(entry.name!==expected)problem('team/path','Request path does not match signed identity');
          outcomes.push(this.apply(envelope));
        }catch(error){
          const record={path:entry.name,object:entry.oid,code:error.code||'team/invalid',message:error.message};
          atomicWrite(path.join(this.directory,'quarantine',entry.oid+'.json'),JSON.stringify(record));outcomes.push({...record,status:'rejected'});
        }
      }
      const commit=await this.transport.publish({[prefix+'snapshot.json']:canonical(this.publication())},'Publish leader-accepted Otoha Graph');
      this.syncFailure=null;this.lastSync=new Date().toISOString();return {ok:true,commit,cursor:this.authority.record().cursor,outcomes};
    }catch(error){this.syncFailure={code:error.code||'team/transport',message:error.message};throw error;}
  }
  teamState(){return {role:'leader',actor:this.config.actor,cursor:this.authority.record().cursor,syncFailure:this.syncFailure,lastSync:this.lastSync||null,...this.authority.snapshot().collaboration};}
}
