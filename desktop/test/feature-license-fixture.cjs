'use strict';
// Test-only issuer support. The private key is supplied outside the repository.
const fs=require('node:fs'),crypto=require('node:crypto'),path=require('node:path');
const {BridgeService}=require('../lib/service.cjs');
async function issueFixture(home,{days=1,installation}={}){
 const keyFile=process.env.WISHTOKEN_TEST_ISSUER_KEY;if(!keyFile)throw Error('Set WISHTOKEN_TEST_ISSUER_KEY to an owner-side test issuer key');
 const service=new BridgeService({home});service.config=JSON.parse(fs.readFileSync(path.join(home,'config.json'),'utf8'));
 const status=await service.request('/api/features/fenjue/license');const now=Math.floor(Date.now()/1000);
 const claims={v:1,product:'wishtoken-desktop',feature:'fenjue',installation:installation||status.installation,id:'synthetic-ui-'+crypto.randomUUID(),issued_at:days<0?now-172800:now-1,expires_at:now+days*86400};
 const payload='WTK1.'+Buffer.from(JSON.stringify(claims)).toString('base64url');
 return payload+'.'+crypto.sign(null,Buffer.from(payload),fs.readFileSync(keyFile)).toString('base64url');
}
async function unlockFixture(home){const code=await issueFixture(home);const service=new BridgeService({home});service.config=JSON.parse(fs.readFileSync(path.join(home,'config.json'),'utf8'));return service.request('/api/features/fenjue/license','POST',{code});}
function stopFixtureProcess(app){
 const child=app.process();if(child.exitCode!==null)return;
 try{if(process.platform==='win32')require('node:child_process').execFileSync('taskkill.exe',['/PID',String(child.pid),'/T','/F'],{windowsHide:true,stdio:'ignore'});else child.kill('SIGKILL');}
 catch(error){try{process.kill(child.pid,0);}catch(probe){if(probe.code==='ESRCH')return;throw probe;}throw error;}
}
module.exports={issueFixture,unlockFixture,stopFixtureProcess};
