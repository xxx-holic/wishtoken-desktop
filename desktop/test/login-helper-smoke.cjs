'use strict';
const assert=require('node:assert/strict'),path=require('node:path'),{spawnSync}=require('node:child_process');
const folder=`${{win32:'win',darwin:'mac',linux:'linux'}[process.platform]}-${process.arch}`;
const binary=process.argv[2]||path.join(__dirname,'../login-helper',folder,process.platform==='win32'?'wish-login-http.exe':'wish-login-http');
const inputs=[{id:1,kind:'init',proxy:''},{id:2,kind:'cookies',url:'https://auth.openai.com'},
 {id:3,kind:'request',url:'https://example.invalid/private-fixture',options:{method:'POST',body:'synthetic-private-password'}},
 {id:4,kind:'cookies',url:'https://auth.openai.com@invalid.example/'}];
const result=spawnSync(binary,[],{input:inputs.map(x=>JSON.stringify(x)).join('\n')+'\n',encoding:'utf8',timeout:30000,windowsHide:true,env:{...process.env,PYTHONPATH:'',PYTHONHOME:''}});
assert.equal(result.status,0,result.error?.message||'Helper did not finish');
const messages=result.stdout.trim().split(/\r?\n/).map(line=>JSON.parse(line));
assert.equal(messages.length,4);assert.ok(messages[0].ok&&messages[0].data.userAgent);assert.deepEqual(messages[1].data,[]);
for(const m of messages.slice(2)){assert.equal(m.ok,false);assert.equal(m.error,'protocol_transport_error');}
assert.ok(!result.stdout.includes('synthetic-private-password'));assert.equal(result.stderr,'');
console.log(JSON.stringify({ok:true,bundledTransport:true,isolatedCookies:true,destinationAllowlist:true,redactedErrors:true,networkRequests:0}));
