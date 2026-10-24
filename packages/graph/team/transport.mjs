import fs from 'node:fs';
import path from 'node:path';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { randomUUID } from 'node:crypto';
import { problem } from '../design/model.mjs';
const run=promisify(execFile);

// Git is only a mailbox and publication transport. Never checkout/merge remote
// source, policy, executables, hooks or a worktree on either participant.
export class GitTransport {
  constructor(directory,remote,branch='otoha-sync'){
    this.directory=path.join(directory,'transport.git');this.remote=remote;this.branch=branch;
    if(typeof remote!=='string'||remote.startsWith('-')||/[\r\n]/.test(remote)||remote.startsWith('ext::'))problem('team/remote','Invalid remote');
    if(!/^[a-zA-Z0-9][a-zA-Z0-9_/-]*$/.test(branch)||branch.includes('//'))problem('team/branch','Invalid sync branch');
  }
  async git(args,options={}){
    return (await run('git',['-c','core.hooksPath=/dev/null','-c','protocol.ext.allow=never',...args],{cwd:this.directory,timeout:30000,maxBuffer:32*1024*1024,...options,env:{...process.env,GIT_TERMINAL_PROMPT:'0',GIT_AUTHOR_NAME:'Otoha Graph',GIT_AUTHOR_EMAIL:'otoha-graph@localhost',GIT_COMMITTER_NAME:'Otoha Graph',GIT_COMMITTER_EMAIL:'otoha-graph@localhost',...options.env}})).stdout;
  }
  async init(){if(!fs.existsSync(this.directory)){fs.mkdirSync(this.directory,{recursive:true});await this.git(['init','--bare','--quiet']);}}
  async fetch(){
    await this.init();const ref='refs/heads/'+this.branch;
    const listing=await this.git(['ls-remote','--heads',this.remote,ref]);if(!listing.trim())return null;
    await this.git(['fetch','--quiet','--no-tags',this.remote,'+'+ref+':refs/otoha/inbox']);
    return (await this.git(['rev-parse','refs/otoha/inbox'])).trim();
  }
  async list(head,prefix){
    if(!head)return [];
    const out=await this.git(['ls-tree','-r','-z',head,'--',prefix]);
    const entries=out.split('\0').filter(Boolean).map(line=>{const [meta,name]=line.split('\t');const [mode,type,oid]=meta.split(' ');return {mode,type,oid,name};});
    if(entries.length>10000)problem('team/inbox-size','Too many transport entries; archive processed requests explicitly');
    return entries;
  }
  async read(head,file,maxBytes=65536){
    const entries=await this.list(head,file),entry=entries.find(e=>e.name===file);if(!entry)return null;
    if(entry.mode!=='100644'||entry.type!=='blob')problem('team/transport-file','Expected a regular data file');
    const size=Number((await this.git(['cat-file','-s',entry.oid])).trim());if(size>maxBytes)problem('team/size','Transport file too large');
    return await this.git(['cat-file','blob',entry.oid]);
  }
  async publish(files,message='Otoha Graph synchronization'){
    // Normal fast-forward pushes reject a competing publication. Rebase only
    // the outgoing data edits onto the newest tree, never the local authority.
    for(let attempt=0;attempt<3;attempt++){
      const head=await this.fetch(),index=path.join(this.directory,'index-'+randomUUID());
      const opts={env:{GIT_INDEX_FILE:index}};
      try{
        await this.git(head?['read-tree',head]:['read-tree','--empty'],opts);
        for(const [file,bytes] of Object.entries(files)){
          if(!/^otoha\/[a-zA-Z0-9_-]+\/(snapshot\.json|requests\/[a-zA-Z0-9_-]+\/[a-zA-Z0-9_-]+\.json)$/.test(file))problem('team/path','Unexpected transport path');
          // execFile has no stdin option; write only our own data in the private cache.
          const tmp=path.join(this.directory,'blob-'+randomUUID());fs.writeFileSync(tmp,bytes,{mode:0o600});
          let blob;try{blob=(await this.git(['hash-object','-w',tmp])).trim();}finally{fs.unlinkSync(tmp);}
          await this.git(['update-index','--add','--cacheinfo','100644',blob,file],opts);
        }
        const tree=(await this.git(['write-tree'],opts)).trim();
        if(head&&tree===(await this.git(['rev-parse',head+'^{tree}'])).trim())return head;
        const commit=(await this.git(['commit-tree',tree,...(head?['-p',head]:[]),'-m',message])).trim();
        try{await this.git(['push','--quiet',this.remote,commit+':refs/heads/'+this.branch]);return commit;}
        catch(error){if(attempt===2)throw error;}
      }finally{if(fs.existsSync(index))fs.unlinkSync(index);}
    }
  }
}
