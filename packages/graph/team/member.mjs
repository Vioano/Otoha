import fs from 'node:fs';
import path from 'node:path';
import { randomUUID, createPublicKey } from 'node:crypto';
import { GraphAuthority, atomicWrite } from '../design/authority.mjs';
import { parseModel, problem } from '../design/model.mjs';
import { buildDesign, explorerHTML } from '../design/deliver.mjs';
import { queryGraph, manifest, diffSnapshots, paginate } from '../design/query.mjs';
import { canonical, hash, id, exact, initializeIdentity, readJSON, signed, verified, validateRequest, publicKey } from './protocol.mjs';
import { GitTransport } from './transport.mjs';

export function initMember(options){
  const invitation=options.invitation;
  exact(invitation,['version','projectId','epoch','leaderKey','remote','branch']);
  if(invitation.version!==1||!id(invitation.projectId)||typeof invitation.epoch!=='string'||!invitation.epoch||invitation.epoch.length>100)problem('team/invite','Invalid invitation');publicKey(invitation.leaderKey);
  new GitTransport(options.directory,invitation.remote,invitation.branch);
  const directory=path.resolve(options.directory),identity=initializeIdentity(directory,options.actor);
  atomicWrite(path.join(directory,'config.json'),JSON.stringify({...invitation,role:'member',actor:identity.actor}));return identity;
}
export class TeamMember {
  constructor(directory,{readOnly=false}={}){
    this.directory=path.resolve(directory);this.config=readJSON(path.join(directory,'config.json'));
    if(this.config.role!=='member')problem('team/role','Expected member installation');
    this.key=fs.readFileSync(path.join(directory,'private.pem'),'utf8');this.transport=new GitTransport(directory,this.config.remote,this.config.branch);this.coordinationDirectory=this.directory;this.writer=false;this.lockToken=randomUUID();
    this.records=new Map();this.syncFailure=null;this.authority=this;this.current=null;this.failure=null;this.directoryGraph=path.join(directory,'replica');this.options={input:path.join(directory,'verified-model.json')};
    fs.mkdirSync(this.directoryGraph,{recursive:true});
    if(!readOnly)this.acquire();
    try{for(const name of fs.readdirSync(this.directoryGraph).filter(n=>/^\d{12}\.json$/.test(n)).sort()){
      try{this.accept(readJSON(path.join(this.directoryGraph,name)),false);}
      catch(error){this.failure={code:'team/cache-damaged',message:'Damaged replica retained for inspection; using last verified version',file:name};}
    }}catch(error){this.close();throw error;}
  }
  acquire(){GraphAuthority.prototype.acquire.call(this);}
  close(){GraphAuthority.prototype.close.call(this);}
  writable(){if(!this.writer)problem('authority/read-only','Replica updates require its local writer');}
  refresh(){return false;}
  record(cursor){const n=cursor===undefined?this.current?.cursor:Number(cursor);if(!this.records.has(n))problem('authority/reset-required','Verified version unavailable; sync first',{latestCursor:this.current?.cursor||0},409);return this.records.get(n).payload.snapshot;}
  snapshot(cursor){return structuredClone(this.record(cursor));}
  status(){return {cursor:this.current?.cursor||0,revision:this.current?.revision||null,evidenceRevision:this.current?.evidenceRevision||null,failure:this.failure,readOnly:true,role:'member'};}
  manifest(){return {...manifest(this.snapshot()),authority:this.status()};}
  query(q={}){return {...queryGraph(this.snapshot(q.cursor),q),authority:this.status()};}
  diff(after,before,options={}){return diffSnapshots(this.snapshot(after),this.snapshot(before),options);}
  history(options={}){const cursor=this.record(options.cursor).cursor;return {cursor,...paginate([...this.records.keys()].filter(n=>n<=cursor).map(cursor=>({cursor,revision:this.record(cursor).revision})),{cursor},options)};}
  events(after=0){if(Number(after)!==0)this.record(after);return [...this.records.keys()].filter(n=>n>Number(after)).map(cursor=>({cursor,revision:this.record(cursor).revision}));}
  bundle(cursor){const snapshot=this.snapshot(cursor);let views=this.viewCache?.cursor===snapshot.cursor?this.viewCache.views:null;
    if(!views){views=buildDesign(this.options.input,{loaded:parseModel(this.records.get(snapshot.cursor).payload.source)}).views;this.viewCache={cursor:snapshot.cursor,views};}
    return {snapshot,views};
  }
  html(){const b=this.bundle();return explorerHTML(b.snapshot,b.views);}
  rollback(){problem('team/forbidden','Only the leader may roll back the authoritative graph',{},403);}
  accept(envelope,persist=true){
    if(persist)this.writable();
    const p=verified(envelope,this.config.leaderKey);
    if(p.version!==1||p.projectId!==this.config.projectId||p.epoch!==this.config.epoch||p.snapshot?.collaboration?.epoch!==p.epoch||p.cursor!==p.snapshot?.cursor||!Number.isInteger(p.cursor)||p.cursor<1)problem('team/publication','Publication identity mismatch');
    const loaded=parseModel(p.source);
    if(loaded.revision!==p.snapshot.revision)problem('team/publication','Publication source hash mismatch');
    if(this.current&&p.cursor<this.current.cursor)problem('team/replay','Remote publication is older than the verified local version',{},409);
    if(this.records.has(p.cursor)&&canonical(this.records.get(p.cursor))!==canonical(envelope))problem('team/equivocation','Different publication reused the same version',{},409);
    // Validate renderability before switching either Human or Agent projection.
    if(!this.records.has(p.cursor))buildDesign(this.options.input,{loaded});
    if(persist)atomicWrite(path.join(this.directoryGraph,String(p.cursor).padStart(12,'0')+'.json'),canonical(envelope));
    this.records.set(p.cursor,envelope);this.current=p.snapshot;this.failure=null;
  }
  prepare(changes,context={},requestId=randomUUID()){
    this.writable();const s=this.snapshot();
    if(!Array.isArray(changes))problem('team/request','Expected an array of changes');
    if(!id(requestId))problem('team/request','Invalid request ID');
    const existing=path.join(this.directory,'outbox',requestId+'.json');
    if(fs.existsSync(existing)){
      const prior=this.readOutboxRequest(requestId+'.json'),p=prior.payload;
      const retried=changes.map(c=>c.operation==='field.set'?{...c,expectedVersion:c.expectedVersion??p.changes.find(x=>x.nodeId===c.nodeId&&x.field===c.field)?.expectedVersion}:c);
      if(canonical(retried)!==canonical(p.changes)||canonical(context)!==canonical(p.context))problem('team/idempotency-conflict','Request ID already exists with another intent',{},409);
      return prior;
    }
    const request=validateRequest({version:1,projectId:this.config.projectId,epoch:this.config.epoch,requestId,actor:this.config.actor,baseCursor:s.cursor,changes:changes.map(c=>c.operation==='field.set'?{...c,expectedVersion:c.expectedVersion??s.collaboration.fieldVersions[c.nodeId+':'+c.field]}:c),context});
    const envelope=signed(request,this.key),file=path.join(this.directory,'outbox',requestId+'.json');
    if(fs.existsSync(file)&&canonical(readJSON(file))!==canonical(envelope))problem('team/idempotency-conflict','Request ID already exists locally',{},409);
    atomicWrite(file,canonical(envelope));return envelope;
  }
  readOutboxRequest(file){
    if(!file.endsWith('.json')||!id(file.slice(0,-5)))problem('team/outbox-identity','Invalid outbox filename');
    const target=path.join(this.directory,'outbox',file),stat=fs.lstatSync(target);
    if(!stat.isFile())problem('team/outbox-file','Outbox entry must be a regular file');
    if(stat.size>70000)problem('team/size','Outbox file exceeds protocol limit');
    const envelope=readJSON(target,70000);
    const key=createPublicKey(this.key).export({type:'spki',format:'pem'});
    const request=validateRequest(verified(envelope,key));
    if(request.actor!==this.config.actor||request.projectId!==this.config.projectId||request.epoch!==this.config.epoch||request.requestId!==file.slice(0,-5))problem('team/outbox-identity','Outbox request identity does not match this member and filename');
    return envelope;
  }
  readOutbox(){
    const directory=path.join(this.directory,'outbox'),requests=[],errors=[];
    if(fs.existsSync(directory))for(const file of fs.readdirSync(directory).filter(n=>n.endsWith('.json')).sort()){
      try{requests.push({file,envelope:this.readOutboxRequest(file)});}
      catch(error){errors.push({file,code:error.code||'team/outbox-invalid',message:error instanceof SyntaxError?'Invalid JSON in retained outbox file':error.message});}
    }
    // Keep bad bytes in place for inspection; one entry cannot starve its neighbors.
    return {requests,errors};
  }
  async sync(){if(this.syncing)return this.syncing;this.syncing=this.syncOnce().finally(()=>{this.syncing=null;});return this.syncing;}
  async syncOnce(){
    this.writable();try{
      const head=await this.transport.fetch(),prefix='otoha/'+this.config.projectId+'/';
      const remote=await this.transport.read(head,prefix+'snapshot.json',32*1024*1024);if(!remote)problem('team/not-published','Leader has not published a snapshot');
      this.accept(JSON.parse(remote));
      const {requests,errors}=this.readOutbox(),files={};
      for(const {file,envelope:request} of requests){
        if(!this.current.collaboration.receipts.some(r=>r.actor===this.config.actor&&r.requestId===request.payload.requestId&&r.payloadHash===hash(request.payload)))files[prefix+'requests/'+this.config.actor+'/'+file]=canonical(request);
      }
      if(Object.keys(files).length)await this.transport.publish(files,'Submit Otoha Graph change requests');
      this.syncFailure=null;this.failure=null;this.lastSync=new Date().toISOString();return {ok:!errors.length,partial:!!errors.length,cursor:this.current.cursor,submitted:Object.keys(files).length,outboxErrors:errors};
    }catch(error){this.syncFailure={code:error.code||'team/transport',message:error.message};this.failure=this.syncFailure;throw error;}
  }
  teamState(){
    const {requests,errors}=this.readOutbox();
    return {role:'member',actor:this.config.actor,cursor:this.current?.cursor||0,syncFailure:this.syncFailure,lastSync:this.lastSync||null,...(this.current?.collaboration||{}),outboxErrors:errors,outbox:requests.map(({envelope:e})=>({requestId:e.payload.requestId,changes:e.payload.changes,receipt:this.current?.collaboration.receipts.find(r=>r.actor===this.config.actor&&r.requestId===e.payload.requestId&&r.payloadHash===hash(e.payload))||null}))};
  }
}
