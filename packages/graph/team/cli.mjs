import fs from 'node:fs';
import path from 'node:path';
import { initLeader, TeamLeader } from './leader.mjs';
import { initMember, TeamMember } from './member.mjs';
import { readJSON } from './protocol.mjs';
import { problem } from '../design/model.mjs';
import { readAuthority, connectAuthority, requestAuthority } from '../design/client.mjs';
import { startDesignPreview } from '../design/server.mjs';

const help=`Otoha Graph team · leader computer is authoritative
  team init-leader --state PRIVATE_DIR --model system.json --project ID --remote GIT_URL [--repo-root PATH] [--actor leader] [--branch otoha-sync]
  team invite --state LEADER_DIR                    # share this trusted public invitation
  team init-member --state PRIVATE_DIR --actor alice --invite invite.json
  team identity --state DIR                        # share public identity with leader
  team grant --state LEADER_DIR --payload grant.json
  team request --state MEMBER_DIR --payload changes.json
  team sync --state DIR                            # one bounded synchronization
  team serve --state DIR [--port 0] [--interval 10] # local viewer and ongoing sync
  team state --state DIR
  team query --state DIR --mode overview|local|view|full|board [--target ID] [--view ID] [--cursor N] [--page TOKEN]
  team query --state DIR --mode board [--assignee NAME] [--status todo|doing|review|done] [--search TEXT] [--filter all|active|blocked|review|unassigned]
  team manifest --state DIR

Private state MUST stay outside Git worktrees. Never git pull into leader state.
See references/collaboration.md for enrollment, permissions, recovery and limitations.`;
export async function runTeam(args){
  let team;
  try{
    const [command,...rest]=args;if(!command||['help','--help','-h'].includes(command)){console.log(help);return;}
    const options={},query={};
    for(let i=0;i<rest.length;i++){
      const key=rest[i],value=rest[++i];if(!key.startsWith('--')||value===undefined||value.startsWith('--'))problem('team/usage',help);
      if(['--state','--model','--repo-root','--project','--remote','--actor','--branch','--invite','--payload','--port','--interval'].includes(key))options[key.slice(2)]=value;
      else if(['--mode','--target','--from','--to','--view','--expanded','--depth','--hops','--direction','--kinds','--detail','--cursor','--limit','--max-bytes','--page','--assignee','--status','--search','--filter'].includes(key))query[key==='--max-bytes'?'maxBytes':key.slice(2)]=value;
      else problem('team/usage','Unknown option '+key);
    }
    if(!options.state)problem('team/usage','--state is required');const directory=path.resolve(options.state);let result;
    if(command==='init-leader')result=initLeader({directory,input:options.model,repoRoot:options['repo-root'],projectId:options.project,remote:options.remote,actor:options.actor,branch:options.branch});
    else if(command==='init-member')result=initMember({directory,actor:options.actor,invitation:readJSON(options.invite)});
    else if(command==='identity')result=readJSON(path.join(directory,'identity.json'));
    else{
      const config=readJSON(path.join(directory,'config.json')),isLeader=config.role==='leader',input=path.join(directory,'model.json');
      if(command==='invite'&&isLeader){const {projectId,epoch,leaderKey,remote,branch}=config;result={version:1,projectId,epoch,leaderKey,remote,branch};}
      else if(['query','manifest','state'].includes(command)&&isLeader){
        const response=await readAuthority({input,stateDir:path.join(directory,'graph')},command==='state'?'inspect':command,query);
        result=command==='state'?{role:'leader',actor:config.actor,cursor:response.cursor,...response.collaboration,connection:response.connection,...(response.warning?{warning:response.warning}:{})}:response;
      }else{
        try{team=isLeader?new TeamLeader(directory):new TeamMember(directory,{readOnly:['query','manifest','state'].includes(command)});}
        catch(error){
          if(error.code!=='authority/locked'||!['sync','grant','request'].includes(command))throw error;
          result=await requestAuthority(await connectAuthority({input:isLeader?input:path.join(directory,'verified-model.json'),stateDir:isLeader?path.join(directory,'graph'):directory,...(!isLeader?{url:readJSON(path.join(directory,'session.json')).url}:{}),requireLive:true}),'/api/team/'+command,{payload:['grant','request'].includes(command)?readJSON(options.payload):{}});
        }
        if(team){
          if(command==='invite')result=team.invite();
          else if(command==='grant'){if(!isLeader)problem('team/forbidden','Only the leader may grant permissions',{},403);result={ok:true,cursor:team.grant(readJSON(options.payload)).cursor};}
          else if(command==='request'){if(isLeader)problem('team/role','Use local model editing for leader changes');const p=readJSON(options.payload);result=team.prepare(p.changes,p.context,p.requestId);}
          else if(command==='sync')result=await team.sync();
          else if(command==='query')result=team.authority.query(query);
          else if(command==='manifest')result=team.authority.manifest();
          else if(command==='state')result=team.teamState();
          else if(command==='serve'){
            const interval=Number(options.interval||10);if(!Number.isFinite(interval)||interval<2||interval>3600)problem('team/interval','Use an interval from 2 to 3600 seconds');
            const port=Number(options.port||0);if(!Number.isInteger(port)||port<0||port>65535)problem('team/port','Invalid port');
            try{await team.sync();}catch(error){if(!team.authority.current)throw error;}
            const server=await startDesignPreview({input:team.authority.options.input,repoRoot:config.repoRoot,port,team});
            console.log(JSON.stringify({ok:true,role:config.role,url:server.url,state:directory}));
            let timer,stopped=false,failures=0;
            const tick=async()=>{try{await team.sync();failures=0;}catch{failures++;}if(!stopped)timer=setTimeout(tick,Math.min(60000,interval*1000*2**Math.min(failures,3)));};
            timer=setTimeout(tick,interval*1000);
            const stop=async()=>{if(stopped)return;stopped=true;clearTimeout(timer);if(team.syncing)await team.syncing.catch(()=>{});await server.stop();process.exit(0);};
            process.once('SIGINT',stop);process.once('SIGTERM',stop);return;
          }else problem('team/usage',help);
        }
      }
    }
    // Member reads open a verified disk replica, not the serving process. Null
    // lastSync/syncFailure from this short-lived reader are not live diagnostics.
    if(team?.config?.role==='member'&&['query','manifest','state'].includes(command))result={...result,connection:'local-accepted-snapshot',warning:'Verified local cache only; this command does not synchronize or report the running server connection.'};
    console.log(JSON.stringify(result,null,2));team?.close();
  }catch(error){team?.close();console.error(JSON.stringify({ok:false,code:error.code||'team/error',message:error.message,details:error.details},null,2));process.exitCode=1;}
}
