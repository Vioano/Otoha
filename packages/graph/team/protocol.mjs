import fs from 'node:fs';
import path from 'node:path';
import { generateKeyPairSync, createPublicKey, sign, verify } from 'node:crypto';
import { problem, digest } from '../design/model.mjs';
import { atomicWrite } from '../design/authority.mjs';

import { taskFields } from '../design/tasks.mjs';

export const fields=['label','purpose','inputs','outputs','steps','openIssues'];
export const id=value=>typeof value==='string'&&/^[a-zA-Z0-9][a-zA-Z0-9_-]{0,99}$/.test(value);
export const canonical=value=>JSON.stringify(value,(_key,item)=>item&&typeof item==='object'&&!Array.isArray(item)?Object.fromEntries(Object.keys(item).sort().map(key=>[key,item[key]])):item);
export const hash=value=>digest(canonical(value));
const message=payload=>Buffer.from('otoha-graph-team-v1\n'+canonical(payload));
export function exact(value,keys){if(!value||typeof value!=='object'||Array.isArray(value)||Object.keys(value).some(k=>!keys.includes(k)))problem('team/shape','Unknown or malformed protocol fields');}
export function publicKey(value){try{const key=createPublicKey(value);if(key.asymmetricKeyType!=='ed25519')throw Error();return key;}catch{problem('team/key','Expected an Ed25519 public key');}}
export function signed(payload,key){return {payload,signature:sign(null,message(payload),key).toString('base64')};}
export function verified(envelope,key){
  exact(envelope,['payload','signature']);
  if(typeof envelope.signature!=='string'||!verify(null,message(envelope.payload),publicKey(key),Buffer.from(envelope.signature,'base64')))problem('team/signature','Signature does not match the enrolled identity',{},403);
  return envelope.payload;
}
export function readJSON(file,max=32*1024*1024){const bytes=fs.readFileSync(file);if(bytes.length>max)problem('team/size','File exceeds protocol limit');return JSON.parse(bytes);}
export function initializeIdentity(directory,actor){
  if(!id(actor))problem('team/actor','Invalid actor ID');
  if(fs.existsSync(directory)&&fs.readdirSync(directory).length)problem('team/exists','State directory must be new or empty');
  fs.mkdirSync(directory,{recursive:true,mode:0o700});
  // A private authority cannot be a file in the repository it is meant to defend.
  for(let p=fs.realpathSync(directory);;p=path.dirname(p)){
    if(fs.existsSync(path.join(p,'.git')))problem('team/state-location','Keep team state and private keys outside all Git worktrees');
    if(path.dirname(p)===p)break;
  }
  const keys=generateKeyPairSync('ed25519',{publicKeyEncoding:{type:'spki',format:'pem'},privateKeyEncoding:{type:'pkcs8',format:'pem'}});
  atomicWrite(path.join(directory,'private.pem'),keys.privateKey);
  atomicWrite(path.join(directory,'identity.json'),JSON.stringify({actor,publicKey:keys.publicKey},null,2));
  return {actor,publicKey:keys.publicKey};
}
export function validateGrant(grant){
  exact(grant,['actor','publicKey','grants','agentId','sessionId','taskGrants']);
  if(!id(grant.actor))problem('team/policy','Invalid actor');publicKey(grant.publicKey);
  if(!Array.isArray(grant.grants)||grant.grants.length>100)problem('team/policy','Expected bounded grants');
  for(const rule of grant.grants){
    exact(rule,['nodes','fields','comments','onlyIfEmpty']);
    if(!Array.isArray(rule.nodes)||!rule.nodes.length||rule.nodes.some(n=>!id(n)))problem('team/policy','Grant explicit node IDs');
    if(!Array.isArray(rule.fields)||rule.fields.some(f=>!fields.includes(f)))problem('team/policy','Unknown editable field');
    for(const key of ['comments','onlyIfEmpty'])if(rule[key]!==undefined&&typeof rule[key]!=='boolean')problem('team/policy','Expected boolean permission');
  }
  if(grant.taskGrants!==undefined){
    if(!Array.isArray(grant.taskGrants)||grant.taskGrants.length>100)problem('team/policy','Expected bounded task grants');
    for(const rule of grant.taskGrants){
      exact(rule,['tasks','fields']);
      if(!Array.isArray(rule.tasks)||!rule.tasks.length||rule.tasks.length>2000||rule.tasks.some(t=>!id(t)))problem('team/policy','Grant explicit task IDs');
      if(!Array.isArray(rule.fields)||rule.fields.some(f=>!taskFields.includes(f)))problem('team/policy','Unknown editable task field');
    }
  }
  for(const key of ['agentId','sessionId'])if(grant[key]!==undefined&&(typeof grant[key]!=='string'||grant[key].length>200))problem('team/policy','Invalid assignment context');
  return grant;
}
export function validateRequest(request){
  exact(request,['version','projectId','epoch','requestId','actor','baseCursor','changes','context']);
  if(request.version!==1||!id(request.projectId)||!id(request.actor)||!id(request.requestId)||typeof request.epoch!=='string'||request.epoch.length>100||!Number.isInteger(request.baseCursor)||request.baseCursor<1)problem('team/request','Invalid request identity or base version');
  if(!Array.isArray(request.changes)||!request.changes.length||request.changes.length>20)problem('team/request','Use 1–20 explicit changes');
  exact(request.context||{},['agentId','sessionId']);
  for(const value of Object.values(request.context||{}))if(typeof value!=='string'||value.length>200)problem('team/request','Invalid session context');
  const seen=new Set();
  for(const change of request.changes){
    if(change.operation==='task.set'){
      exact(change,['operation','taskId','field','expectedVersion','value']);
      if(!id(change.taskId)||!taskFields.includes(change.field)||!Number.isInteger(change.expectedVersion)||change.expectedVersion<1)problem('team/operation','Task edits require declared fields and versions');
      const key='task:'+change.taskId+':'+change.field;if(seen.has(key))problem('team/request','Duplicate task field');seen.add(key);continue;
    }
    exact(change,change.operation==='comment.add'?['operation','nodeId','text']:['operation','nodeId','field','expectedVersion','value']);
    if(!id(change.nodeId))problem('team/request','Invalid node ID');
    if(change.operation==='field.set'){
      if(!fields.includes(change.field)||!Number.isInteger(change.expectedVersion)||change.expectedVersion<1)problem('team/operation','Only declared fields with explicit versions may be changed');
      const key=change.nodeId+':'+change.field;if(seen.has(key))problem('team/request','Duplicate field in atomic request');seen.add(key);
    }else if(change.operation==='comment.add'){
      if(typeof change.text!=='string'||!change.text.trim()||change.text.length>4000)problem('team/request','Comment must contain 1–4000 characters');
    }else problem('team/operation','Unsupported member operation');
  }
  if(Buffer.byteLength(canonical(request))>65536)problem('team/size','Request exceeds 64 KiB');
  return request;
}
