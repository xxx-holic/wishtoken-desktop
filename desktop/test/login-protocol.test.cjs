'use strict';
const test=require('node:test'),assert=require('node:assert/strict');
const {LoginProtocol,authURL,nextURL,factorID,workspaces,fnvMix}=require('../lib/login-protocol.cjs');
const {AutoLogin}=require('../lib/auto-login.cjs');
const {localCallback}=require('../lib/login-curl.cjs');
const AUTH='https://auth.openai.com';
const login={channel:'codex',session:'123',auth_url:AUTH+'/oauth/authorize?client_id=app_EMoamEEZ73f0CkXaXp7hrann&redirect_uri=http%3A%2F%2Flocalhost%3A1455%2Fauth%2Fcallback&state=fixture-state'};
function fixture(overrides={}) {
 const calls=[];let closed=false,calledBack=false;
 const json=data=>({status:200,text:JSON.stringify(data)});
 const http={userAgent:'Synthetic fixture',cookies:async()=>[{name:'oai-did',value:'fixture-device'}],close:async()=>{closed=true;},request:async(url,options={})=>{
  calls.push({url,...options});const p=new URL(url).pathname;
  if(overrides[p])return overrides[p](options);
  if(p==='/oauth/authorize')return json({continue_url:'/log-in',method:'GET'});
  if(p==='/backend-api/sentinel/req')return json({token:'synthetic-security-challenge',proofofwork:{required:false}});
  if(p==='/api/accounts/authorize/continue')return json({continue_url:'/log-in/password'});
  if(p==='/api/accounts/password/verify')return json({continue_url:'/mfa-challenge/fixture-factor',page:{type:'mfa_challenge'}});
  if(p==='/api/accounts/mfa/issue_challenge')return json({});
  if(p==='/api/accounts/mfa/verify')return json({continue_url:'/sign-in-with-chatgpt/codex/consent','oai-client-auth-session':{workspaces:[{id:'personal',title:'Personal'},{id:'team-fixture',title:'Example Team'}]}});
  if(p==='/api/accounts/workspace/select')return json({continue_url:'http://localhost:1455/auth/callback?state=fixture-state&code=synthetic-code'});
  if(p==='/auth/callback'){calledBack=true;return {status:200,text:'saved'};}
  if(['/log-in/password','/mfa-challenge/fixture-factor','/sign-in-with-chatgpt/codex/consent'].includes(p))return {status:200,text:'<html>fixture</html>'};
  throw Error('Unexpected request '+p);
 }};
 return {http,calls,get closed(){return closed;},get calledBack(){return calledBack;}};
}
test('protocol completes email/password/TOTP/workspace/callback without browser or borrowed cookies',async()=>{
 const f=fixture(),stages=[];let chosen=false;
 const driver=new LoginProtocol(f.http,new AbortController().signal,(s)=>stages.push(s),{now:()=>60000});
 await driver.run(login,{email:'child@example.test',password:'fixture-password',seed:'fixture-seed'},async choices=>{chosen=true;return choices[1];},()=> '123456');
 assert.ok(chosen&&f.calledBack);assert.deepEqual(stages,['protocol','email','password','mfa','consent']);
 const body=p=>JSON.parse(f.calls.find(c=>new URL(c.url).pathname===p).body);
 assert.deepEqual(body('/api/accounts/password/verify'),{password:'fixture-password'});
 assert.deepEqual(body('/api/accounts/mfa/verify'),{id:'fixture-factor',type:'totp',code:'123456'});
 assert.deepEqual(body('/api/accounts/workspace/select'),{workspace_id:'team-fixture'});
});
test('protocol is the default; backend save determines ready and cleanup erases credentials',async()=>{
 const f=fixture();let browserCalls=0;
 const manager=new AutoLogin({home:'unused',interval:1,browserFactory:()=>{browserCalls++;throw Error('must not open');},httpFactory:async()=>f.http,service:{config:{},request:async route=>route==='/api/login/start'?login:route==='/api/login/status'?{active:false}:f.calledBack?{done:true,account_id:'acc-fixture'}:{active:true}}});
 const start=await manager.start({channel:'codex',text:'child@example.test----fixture-password----GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ'});
 while(!manager.batch.done){const row=manager.snapshot(start.id).rows[0];if(row.choices.length)manager.choose({id:start.id,index:0,choice:'team-fixture'});await new Promise(r=>setTimeout(r,1));}
 assert.equal(browserCalls,0);assert.equal(manager.snapshot(start.id).rows[0].status,'completed');assert.ok(f.closed);
});
test('protocol distinguishes edge challenge from invalid password and never continues either',async()=>{
 for(const [path,response,expected] of [
  ['/oauth/authorize',{status:403,text:'<html>_cf_chl_opt</html>'},'browser_required'],
  ['/api/accounts/password/verify',{status:401,text:JSON.stringify({error:{code:'incorrect_password'}})},'incorrect_password'],
  ['/api/accounts/password/verify',{status:429,text:''},'rate_limited'],
  ['/api/accounts/workspace/select',{status:401,text:JSON.stringify({error:{code:'deviceauth_disabled',message:'private-fixture'}})},'deviceauth_disabled'],
  ['/api/accounts/workspace/select',{status:409,text:JSON.stringify({error:'auth_page_conflict'})},'auth_page_conflict'],
 ]){
  const f=fixture({[path]:()=>response});const driver=new LoginProtocol(f.http,new AbortController().signal,()=>{}, {now:()=>60000});
  await assert.rejects(driver.run(login,{email:'child@example.test',password:'private-fixture',seed:'s'},async a=>a[0],()=> '123456'),e=>e.loginCode===expected&&!e.message.includes('private-fixture'));
  assert.ok(!f.calledBack);assert.equal(f.calls.filter(c=>new URL(c.url).pathname===path).length,1);
 }
});
test('protocol rejects unknown redirects and wrong callback state before sending anything',async()=>{
 for(const target of ['https://example.test/collect','http://localhost:1455/auth/callback?code=c&state=wrong']){
  const f=fixture({'/api/accounts/workspace/select':()=>({status:200,text:JSON.stringify({continue_url:target})})});
  await assert.rejects(new LoginProtocol(f.http,new AbortController().signal,()=>{}, {now:()=>60000}).run(login,{email:'child@example.test',password:'p',seed:'s'},async a=>a[0],()=> '123456'));
  assert.ok(!f.calls.some(c=>c.url===target));
 }
 for(const url of ['http://localhost:1455/other','http://example.test:1455/auth/callback','http://127.0.0.1:1456/auth/callback'])assert.throws(()=>localCallback(url,new AbortController().signal));
});
test('protocol parsers retain explicit workspace choices and handle modern response envelopes',()=>{
 assert.equal(nextURL({page:{payload:{continue_url:'/workspace'}}}),AUTH+'/workspace');
 assert.equal(factorID({page:{payload:{factors:[{type:'totp',id:'factor'}]}}},'/mfa-challenge'),'factor');
 assert.deepEqual(workspaces({'oai-client-auth-session':{workspaces:[{id:'a',title:'One'},{id:'b',is_personal:true}]}}),[{id:'a',label:'One'},{id:'b',label:'Personal'}]);
 assert.throws(()=>authURL('https://auth.openai.com.example.test/'));
 assert.equal(fnvMix(''),'ab3e7c0b');
});

test('BPS protocol submits the observed form once then waits for backend persistence',async()=>{
 const bps={channel:'bps',session:'456',auth_url:AUTH+'/api/accounts/deviceauth/authorize?client_id=app_fnr0pYvVwwFDocDumLG3H2Bp',user_code:'ABC-123-XYZ'};
 const json=data=>({status:200,text:JSON.stringify(data)});
 let approved=false,polls=0;
 const f=fixture({
  '/api/accounts/deviceauth/authorize':()=>json({continue_url:'/log-in',method:'GET'}),
  '/api/accounts/workspace/select':()=>json({continue_url:'/basispoints/deviceauth/callback?code=fixture-authorization&state=fixture-browser-state'}),
  '/basispoints/deviceauth/callback':()=>({status:200,text:'<html>device approval</html>'}),
  '/api/accounts/deviceauth/authorize_code':options=>{assert.equal(options.headers['Content-Type'],'application/x-www-form-urlencoded;charset=UTF-8');approved=true;return {status:204,text:''};}
 });
 const manager=new AutoLogin({home:'unused',interval:1,httpFactory:async()=>f.http,browserFactory:()=>{throw Error('unexpected browser');},service:{config:{},request:async route=>route==='/api/login/start'?bps:route==='/api/login/status'?{active:false}:approved&&++polls>1?{done:true,account_id:'bps-fixture'}:{active:true}}});
 const start=await manager.start({channel:'bps',text:'child@example.test----fixture-password----GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ'});
 while(!manager.batch.done){const row=manager.snapshot(start.id).rows[0];if(row.choices.length)manager.choose({id:start.id,index:0,choice:'team-fixture'});await new Promise(r=>setTimeout(r,1));}
 assert.equal(manager.snapshot(start.id).rows[0].status,'completed');assert.equal(polls,2);assert.ok(f.closed);
 const approvals=f.calls.filter(c=>new URL(c.url).pathname==='/api/accounts/deviceauth/authorize_code');assert.equal(approvals.length,1);
 const body=new URLSearchParams(approvals[0].body);
 for(const [key,value] of Object.entries({user_code:'ABC-123-XYZ',user_code_text:'ABC123XYZ',device_code:'ABC123XYZ',authorization_code:'fixture-authorization',state:'fixture-browser-state',decision:'grant',client_id:'app_fnr0pYvVwwFDocDumLG3H2Bp',character_1:'A',character_9:'Z'}))assert.equal(body.get(key),value);
 const security=f.calls.filter(c=>new URL(c.url).pathname==='/backend-api/sentinel/req').at(-1);
 assert.equal(JSON.parse(security.body).flow,'device_auth');assert.ok(!f.calledBack);
});

test('BPS device approval rejects missing bindings and failed responses without retry',async()=>{
 const bps={channel:'bps',user_code:'ABC-123-XYZ',auth_url:AUTH+'/api/accounts/deviceauth/authorize?client_id=app_fnr0pYvVwwFDocDumLG3H2Bp'};
 for(const [grant,url] of [[{...bps,user_code:'ABC'},'/basispoints/deviceauth/callback?code=c&state=s'],[bps,'/basispoints/deviceauth/callback?code=c'],[{...bps,channel:'codex'},'/basispoints/deviceauth/callback?code=c&state=s']]){
  const f=fixture();const driver=new LoginProtocol(f.http,new AbortController().signal,()=>{});
  await assert.rejects(driver.approveDevice(grant,{url:AUTH+url}),e=>e.loginCode==='device_approval_invalid');assert.equal(f.calls.length,0);
 }
 const f=fixture({'/api/accounts/deviceauth/authorize_code':()=>({status:400,text:'sensitive upstream error'})});
 await assert.rejects(new LoginProtocol(f.http,new AbortController().signal,()=>{}).approveDevice(bps,{url:AUTH+'/basispoints/deviceauth/callback?code=c&state=s'}),e=>e.loginCode==='device_approval_rejected'&&!e.loginMessage.includes('sensitive'));
 assert.equal(f.calls.filter(c=>new URL(c.url).pathname==='/api/accounts/deviceauth/authorize_code').length,1);
});
