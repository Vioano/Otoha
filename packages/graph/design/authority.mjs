import fs from 'node:fs';
import path from 'node:path';
import { randomUUID } from 'node:crypto';
import { gzipSync, gunzipSync } from 'node:zlib';
import { parseModel, modelSnapshot, digest, problem } from './model.mjs';
import { buildDesign, explorerHTML } from './deliver.mjs';
import { journalPath } from './requests.mjs';
import { manifest, queryGraph, diffSnapshots, paginate, topologyHash } from './query.mjs';
import { taskFields } from './tasks.mjs';

const version=JSON.parse(fs.readFileSync(new URL('../package.json',import.meta.url),'utf8')).version;
export const authorityDirectory=options=>path.join(path.dirname(journalPath(options.input,options.stateDir)),'authority');
const checksum = value => digest(JSON.stringify(value));
function sealed(value){return {...value,checksum:checksum(value)};}
function unseal(value){const {checksum:hash,...body}=value;if(hash!==checksum(body))problem('authority/integrity','Authority record checksum mismatch');return body;}
export function atomicWrite(file,bytes){
  fs.mkdirSync(path.dirname(file),{recursive:true});const tmp=file+'.tmp-'+randomUUID();let fd;
  try{
    fd=fs.openSync(tmp,'wx',0o600);fs.writeFileSync(fd,bytes);fs.fsyncSync(fd);fs.closeSync(fd);fd=undefined;fs.renameSync(tmp,file);
    // Node's Windows directory handles cannot be fsynced (EPERM). File data is
    // still flushed before replacement; rename metadata is not power-loss durable
    // on Windows. Do not catch file/rename errors or weaken other platforms.
    if(process.platform!=='win32'){
      const dir=fs.openSync(path.dirname(file),'r');try{fs.fsyncSync(dir);}finally{fs.closeSync(dir);}
    }
  }
  finally{if(fd!==undefined)fs.closeSync(fd);if(fs.existsSync(tmp))fs.unlinkSync(tmp);}
}
export function sourceFingerprint(input){try{return digest(fs.readFileSync(input));}catch(e){return `missing:${e.code}`;}}
export class GraphAuthority {
  constructor(options){
    this.options={...options,input:path.resolve(options.input)};this.directory=authorityDirectory(this.options);this.coordinationDirectory=authorityDirectory({...this.options,stateDir:undefined});this.records=[];this.current=null;this.failure=null;this.storeFailure=null;this.cache=new Map();this.writer=false;this.lockToken=randomUUID();this.lastEvidenceCheck=0;
    fs.mkdirSync(path.join(this.directory,'commits'),{recursive:true});fs.mkdirSync(path.join(this.directory,'objects'),{recursive:true});
    for(const dir of new Set([this.directory,this.coordinationDirectory])){fs.mkdirSync(dir,{recursive:true});if(!fs.existsSync(path.join(dir,'.gitignore')))atomicWrite(path.join(dir,'.gitignore'),'*\n');}
    const contextFile=path.join(this.directory,'context.json');
    if(fs.existsSync(contextFile)){
      let context;try{context=JSON.parse(fs.readFileSync(contextFile,'utf8'));}catch{problem('authority/context','Unreadable local evidence context; preserve it and repair before reopening');}
      if(context.version!==1||context.input!==this.options.input||!(context.repoRoot===null||typeof context.repoRoot==='string'&&path.isAbsolute(context.repoRoot)))problem('authority/context','Invalid local evidence context');
      if(this.options.repoRoot===undefined)this.options.repoRoot=context.repoRoot;
    }
    if(this.options.repoRoot)this.options.repoRoot=path.resolve(this.options.repoRoot);
    this.readHistory();
  }
  readHistory(){
    const files=fs.readdirSync(path.join(this.directory,'commits')).filter(n=>/^\d{12}\.json$/.test(n)).sort();let previous='';
    for(const name of files){try{
      const record=JSON.parse(fs.readFileSync(path.join(this.directory,'commits',name),'utf8'));const body=unseal(record);
      if(body.cursor!==this.records.length+1||body.previous!==previous)problem('authority/history','Non-contiguous authority history');
      if(body.input!==this.options.input)problem('authority/identity','Authority belongs to a different model');
      this.loadObject(body.object);this.records.push(record);previous=record.checksum;
    }catch(error){this.storeFailure={code:error.code||'authority/history',message:error.message,file:name,recovery:'Preserve the store; restore verified commit/object files from backup. Automatic publication is blocked.'};break;}}
    if(this.records.length)this.current=this.records.at(-1);
  }
  loadObject(hash){
    if(this.cache.has(hash))return this.cache.get(hash);
    if(!/^[a-f0-9]{64}$/.test(hash||''))problem('authority/object','Invalid object identity');
    const bytes=gunzipSync(fs.readFileSync(path.join(this.directory,'objects',hash+'.json.gz')),{maxOutputLength:128*1024*1024});
    if(digest(bytes)!==hash)problem('authority/integrity','Stored graph bundle is damaged');
    const object=JSON.parse(bytes);const loaded=parseModel(object.source);
    if(loaded.revision!==object.snapshot.revision)problem('authority/integrity','Stored source and snapshot revisions differ');
    if(topologyHash(loaded.model)!==topologyHash(object.snapshot.model))problem('authority/integrity','Stored source and snapshot topology differ');
    this.cacheObject(hash,object);return object;
  }
  cacheObject(hash,object){this.cache.set(hash,object);while(this.cache.size>8)this.cache.delete(this.cache.keys().next().value);}
  acquire(){
    const file=path.join(this.coordinationDirectory,'writer.lock');
    for(let attempt=0;attempt<2;attempt++){
      try{const fd=fs.openSync(file,'wx',0o600);try{fs.writeFileSync(fd,JSON.stringify({pid:process.pid,token:this.lockToken}));fs.fsyncSync(fd);}finally{fs.closeSync(fd);}this.writer=true;
        try{atomicWrite(path.join(this.directory,'context.json'),JSON.stringify({version:1,input:this.options.input,repoRoot:this.options.repoRoot||null}));}catch(error){this.close();throw error;}
        return;}
      catch(error){if(error.code!=='EEXIST')throw error;let owner;try{owner=JSON.parse(fs.readFileSync(file,'utf8'));}catch{problem('authority/locked','Unreadable writer lock; preserve and inspect it',{},409);}
        let alive=true;try{process.kill(owner.pid,0);}catch(e){if(e.code==='ESRCH')alive=false;}
        if(alive)problem('authority/locked','Another preview owns this graph. Use its Agent endpoint.',{pid:owner.pid},409);
        fs.renameSync(file,file+'.abandoned-'+randomUUID());
      }
    }
    problem('authority/locked','Could not acquire authority writer',{},409);
  }
  close(){if(!this.writer)return;const lock=path.join(this.coordinationDirectory,'writer.lock');try{const owner=JSON.parse(fs.readFileSync(lock,'utf8'));if(owner.token===this.lockToken)fs.unlinkSync(lock);}catch{}this.writer=false;}
  writable(){if(!this.writer)problem('authority/read-only','Publication requires the authority writer');if(this.storeFailure)problem('authority/store-damaged','Publication blocked by damaged persistent history',this.storeFailure,503);}
  record(cursor){
    if(!this.current)problem('authority/unavailable','No verified graph is available; start preview with a valid model',{},503);
    const n=cursor===undefined?this.current.cursor:Number(cursor);
    if(!Number.isInteger(n)||n<1||n>this.records.length)problem('authority/reset-required','Unknown cursor; obtain the manifest and read a fresh snapshot',{latestCursor:this.current.cursor},409);
    return this.records[n-1];
  }
  snapshot(cursor){const r=this.record(cursor),o=this.loadObject(r.object);return {...o.snapshot,cursor:r.cursor,committedAt:r.at,topologyHash:r.topologyHash,...(o.collaboration?{collaboration:o.collaboration}:{})};}
  bundle(cursor){const r=this.record(cursor),o=this.loadObject(r.object);return {snapshot:this.snapshot(r.cursor),views:o.views};}
  html(){const b=this.bundle();return explorerHTML(b.snapshot,b.views);}
  status(){return {cursor:this.current?.cursor||0,revision:this.current?.revision||null,evidenceRevision:this.current?.evidenceRevision||null,sourceHash:sourceFingerprint(this.options.input),failure:this.storeFailure||this.failure,readOnly:!!this.storeFailure,observedAt:new Date().toISOString(),viewerVersion:version};}
  manifest(){return {...manifest(this.snapshot()),authority:this.status()};}
  query(q={}){return {...queryGraph(this.snapshot(q.cursor),q),authority:this.status()};}
  diff(after,before,options={}){return diffSnapshots(this.snapshot(after),this.snapshot(before),options);}
  history(options={}){const cursor=this.record(options.cursor).cursor;return {cursor,...paginate(this.records.slice(0,cursor).map(({cursor,revision,evidenceRevision,at,reason,rollbackOf,topologyHash})=>({cursor,revision,evidenceRevision,at,reason,rollbackOf,topologyHash})),{historyThrough:cursor},options),authority:this.status()};}
  events(after=0){
    const n=Number(after);if(!Number.isInteger(n)||n<0||n>this.records.length)problem('authority/reset-required','Event cursor is not available',{latestCursor:this.current?.cursor||0},409);
    return this.records.slice(n).map(r=>({cursor:r.cursor,revision:r.revision,evidenceRevision:r.evidenceRevision,at:r.at,reason:r.reason,topologyHash:r.topologyHash}));
  }
  commit(object,reason,extra={}){
    this.writable();
    const prior=this.current?this.loadObject(this.current.object):null;
    if(object.collaboration||prior?.collaboration){
      const collaboration=structuredClone(object.collaboration||prior.collaboration);
      collaboration.fieldVersions||={};
      const old=new Map((prior?.snapshot.model.entities||[]).map(e=>[e.id,e]));
      for(const e of object.snapshot.model.entities)for(const field of ['label','purpose','inputs','outputs','steps','openIssues']){
        const key=e.id+':'+field;
        if(!Object.hasOwn(collaboration.fieldVersions,key)||JSON.stringify(old.get(e.id)?.[field])!==JSON.stringify(e[field]))collaboration.fieldVersions[key]=this.records.length+1;
      }
      const oldTasks=new Map((prior?.snapshot.model.tasks||[]).map(t=>[t.id,t]));
      for(const task of object.snapshot.model.tasks||[])for(const field of taskFields){
        const key='task:'+task.id+':'+field;
        if(!Object.hasOwn(collaboration.fieldVersions,key)||JSON.stringify(oldTasks.get(task.id)?.[field])!==JSON.stringify(task[field]))collaboration.fieldVersions[key]=this.records.length+1;
      }
      object={...object,collaboration};
    }
    const bytes=Buffer.from(JSON.stringify(object)),hash=digest(bytes),file=path.join(this.directory,'objects',hash+'.json.gz');
    if(!fs.existsSync(file))atomicWrite(file,gzipSync(bytes));
    const record=sealed({cursor:this.records.length+1,previous:this.current?.checksum||'',input:this.options.input,object:hash,revision:object.snapshot.revision,evidenceRevision:object.snapshot.evidenceRevision,topologyHash:topologyHash(object.snapshot.model),at:new Date().toISOString(),reason,...extra});
    atomicWrite(path.join(this.directory,'commits',String(record.cursor).padStart(12,'0')+'.json'),JSON.stringify(record));
    this.records.push(record);this.current=record;this.cacheObject(hash,object);this.failure=null;return record;
  }
  prepare(source){
    const loaded=parseModel(source),old=this.current?this.loadObject(this.current.object):null;
    const architecture=model=>JSON.stringify({...model,tasks:undefined});
    // Validated task-only changes do not alter canvas geometry or require five
    // renderer subprocesses for every card move. Re-evaluate evidence normally.
    if(old&&old.viewerVersion===version&&architecture(loaded.model)===architecture(parseModel(old.source).model))return {snapshot:modelSnapshot(loaded,this.options.repoRoot),views:old.views};
    return buildDesign(this.options.input,{...this.options,loaded});
  }
  recoverTransaction(){
    const file=path.join(this.directory,'pending-source.json');if(!fs.existsSync(file))return;
    const pending=JSON.parse(fs.readFileSync(file,'utf8'));
    if(this.records.some(r=>r.transactionId===pending.transactionId)){
      if(![pending.beforeHash,parseModel(pending.source).revision].includes(sourceFingerprint(this.options.input)))problem('authority/recovery-conflict','An unrelated source edit blocks recovery. Preserve it before completing the pending mirror.',{},409);
      atomicWrite(this.options.input,pending.source);
    }
    fs.unlinkSync(file);
  }
  transact(source,reason,extra={}){
    this.writable();this.recoverTransaction();
    const old=this.loadObject(this.record().object),beforeHash=sourceFingerprint(this.options.input);
    if(beforeHash!==old.snapshot.revision)problem('authority/conflict','Working source changed; refresh before retrying',{},409);
    const built=this.prepare(source),transactionId=randomUUID(),file=path.join(this.directory,'pending-source.json');
    if(beforeHash!==sourceFingerprint(this.options.input))problem('authority/conflict','Source changed during preparation',{},409);
    atomicWrite(file,JSON.stringify({transactionId,beforeHash,source}));
    const record=this.commit({...old,source,snapshot:built.snapshot,views:built.views,viewerVersion:version},reason,{transactionId,...extra});
    // Durable commit precedes the editable source mirror; restart finishes it.
    if(![beforeHash,built.snapshot.revision].includes(sourceFingerprint(this.options.input)))problem('authority/recovery-conflict','Source changed after commit; preserve it before recovery',{},409);
    atomicWrite(this.options.input,source);fs.unlinkSync(file);return record;
  }
  refresh({forceEvidence=false}={}){
    if(this.storeFailure)return false;
    const before=this.current?.cursor;
    try{
      this.writable();this.recoverTransaction();const bytes=fs.readFileSync(this.options.input),hash=digest(bytes),old=this.current?this.loadObject(this.current.object):null;
      if(!old||hash!==old.snapshot.revision||old.viewerVersion!==version){
        const built=this.prepare(bytes);
        if(sourceFingerprint(this.options.input)!==hash)problem('authority/superseded','Source changed during validation; keeping the accepted graph');
        this.commit({source:bytes.toString('utf8'),snapshot:built.snapshot,views:built.views,viewerVersion:version},old?'source-update':'initial');this.lastEvidenceCheck=Date.now();
      }else if(forceEvidence||Date.now()-this.lastEvidenceCheck>1000){
        const snapshot=modelSnapshot(parseModel(old.source),this.options.repoRoot);this.lastEvidenceCheck=Date.now();
        if(snapshot.evidenceRevision!==old.snapshot.evidenceRevision)this.commit({...old,snapshot},'evidence-update');
        this.failure=null;
      }else this.failure=null;
    }catch(error){this.failure={code:error.code||'authority/read',message:error.message,details:error.details,recovery:'Fix the source and retry, or rollback to a verified cursor. The last accepted graph remains available.'};}
    return this.current?.cursor!==before;
  }
  rollback(data){
    this.writable();
    for(const key of Object.keys(data))if(!['cursor','expectedCursor','expectedSourceHash','operationId'].includes(key))problem('authority/argument','Unknown rollback field',{key});
    if(typeof data.operationId!=='string'||!/^[a-zA-Z0-9_-]{1,100}$/.test(data.operationId))problem('authority/argument','Rollback requires a stable operationId');
    const payloadHash=checksum(data),duplicate=this.records.find(r=>r.operationId===data.operationId);
    if(duplicate){if(duplicate.payloadHash!==payloadHash)problem('authority/idempotency-conflict','Operation ID has a different payload',{},409);return {ok:true,replayed:true,cursor:duplicate.cursor};}
    if(data.expectedCursor!==this.current?.cursor||data.expectedSourceHash!==sourceFingerprint(this.options.input))problem('authority/conflict','Graph or working source changed; inspect status before retrying',this.status(),409);
    const target=this.loadObject(this.record(data.cursor).object),loaded=parseModel(target.source);
    const built=buildDesign(this.options.input,{...this.options,loaded});
    if(data.expectedSourceHash!==sourceFingerprint(this.options.input))problem('authority/conflict','Working source changed during rollback preparation',this.status(),409);
    let original=null;try{original=fs.readFileSync(this.options.input);}catch(e){if(e.code!=='ENOENT')throw e;}
    const backup=original?path.join(this.directory,'source-backups',digest(original)+'.json'):null;
    if(backup&&!fs.existsSync(backup))atomicWrite(backup,original);
    atomicWrite(this.options.input,loaded.bytes);
    try{const record=this.commit({source:target.source,snapshot:built.snapshot,views:built.views,viewerVersion:version},'rollback',{rollbackOf:Number(data.cursor),operationId:data.operationId,payloadHash,sourceBackup:backup});return {ok:true,cursor:record.cursor,sourceBackup:backup};}
    catch(error){if(original&&sourceFingerprint(this.options.input)===loaded.revision)atomicWrite(this.options.input,original);throw error;}
  }
}

export function discoverAuthority(options){
  const file=path.join(authorityDirectory(options),'session.json');
  if(!fs.existsSync(file))return null;
  try{const session=JSON.parse(fs.readFileSync(file,'utf8'));if(session.input!==path.resolve(options.input))return null;const url=new URL(session.url);if(url.hostname!=='127.0.0.1'||url.protocol!=='http:')return null;return session;}catch{return null;}
}
