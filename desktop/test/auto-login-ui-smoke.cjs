'use strict';
const {_electron:electron,expect}=require('@playwright/test');
const fs=require('node:fs/promises'),path=require('node:path'),os=require('node:os'),net=require('node:net');
const root=path.resolve(__dirname,'../..');
(async()=>{
 const home=await fs.mkdtemp(path.join(os.tmpdir(),'wishtoken-auto-login-ui-'));
 const socket=net.createServer();await new Promise(r=>socket.listen(0,'127.0.0.1',r));const port=socket.address().port;await new Promise(r=>socket.close(r));
 await fs.writeFile(path.join(home,'config.json'),JSON.stringify({listen:`127.0.0.1:${port}`,api_key:'auto-login-fixture-key',auto_refresh:false,usage_probe:false}));
 const client=await electron.launch({executablePath:process.env.GPTBRIDGE_TEST_APP||require('electron'),args:process.env.GPTBRIDGE_TEST_APP?[]:[path.join(root,'desktop')],env:{...process.env,GPTBRIDGE_DESKTOP_HOME:home,GPTBRIDGE_MAIN_CODEX_HOME:path.join(home,'unused-main')}});
 try{
  const page=await client.firstWindow(),errors=[];page.on('pageerror',e=>errors.push(e.message));
  await expect(page.locator('#service-label')).toHaveText('本地服务运行中');
  await client.evaluate(({app})=>{
   const require=process.mainModule.require.bind(process.mainModule),path=require('node:path');
   const {BridgeService}=require(path.join(app.getAppPath(),'lib/service.cjs')),{AutoLogin}=require(path.join(app.getAppPath(),'lib/auto-login.cjs'));
   const original=BridgeService.prototype.request,start=AutoLogin.prototype.start;
   const f=globalThis.autoFixture={session:100,approved:false,saved:false,closed:0,browserCalls:0,cancelled:0,steps:[]};
   BridgeService.prototype.request=async function(route,method,body,...rest){
    if(route==='/api/login/start'){
     f.session++;f.email=body.expected_email;f.channel=body.channel;f.approved=false;f.saved=false;
     return {channel:f.channel,session:String(f.session),auth_url:'https://auth.openai.com/api/accounts/deviceauth/authorize?client_id=app_fnr0pYvVwwFDocDumLG3H2Bp',user_code:'FIX-TUR-123'};
    }
    if(route==='/api/login/status')return {active:false};
    if(route.startsWith('/api/login/cancel')){f.cancelled++;return {ok:true};}
    if(route.startsWith('/api/login/status?')){
     if(f.approved&&!f.saved){
      const token=[Buffer.from('{"alg":"none"}').toString('base64url'),Buffer.from(JSON.stringify({exp:4102444800,email:f.email,sub:'fixture-'+f.session,client_id:'app_fnr0pYvVwwFDocDumLG3H2Bp'})).toString('base64url'),Buffer.from('synthetic-signature').toString('base64url')].join('.');
      f.account=(await original.call(this,'/api/accounts/import?refresh=0','POST',{access_token:token,account_id:'fixture-team'})).ids[0];f.saved=true;
     }
     return {active:!f.saved,done:f.saved,account_id:f.saved?f.account:undefined};
    }
    return original.call(this,route,method,body,...rest);
   };
   AutoLogin.prototype.start=function(input){
    this.interval=10;
    this.browserFactory=async()=>{f.browserCalls++;throw Error('unexpected browser');};
    this.httpFactory=async({signal})=>({userAgent:'Synthetic fixture',cookies:async()=>[],close:async()=>{f.closed++;},request:async(url,options={})=>{
     signal.throwIfAborted();const p=new URL(url).pathname;f.steps.push(p);
     const json=data=>({status:200,text:JSON.stringify(data)});
     if(p==='/api/accounts/deviceauth/authorize')return json({continue_url:'/log-in',method:'GET'});
     if(p==='/backend-api/sentinel/req')return json({token:'synthetic-challenge',proofofwork:{required:false}});
     if(p==='/api/accounts/authorize/continue')return json({continue_url:'/log-in/password'});
     if(p==='/api/accounts/password/verify')return JSON.parse(options.body).password==='bad-fixture'?{status:401,text:'{"error":{"code":"incorrect_password"}}'}:json({continue_url:'/workspace','oai-client-auth-session':{workspaces:[{id:'personal',title:'Personal'},{id:'fixture-team',title:'Example Team'}]}});
     if(p==='/api/accounts/workspace/select')return json({continue_url:'/basispoints/deviceauth/callback?code=fixture&state=fixture'});
     if(p==='/api/accounts/deviceauth/authorize_code'){f.approved=true;return json({status:'success'});}
     return {status:200,text:'<html>synthetic page</html>'};
    }});
    return start.call(this,input);
   };
  });
  await page.locator('#login-account').click();
  await expect(page.locator('#auto-login-mode')).toHaveValue('protocol');
  await page.locator('#auto-login-input').fill('invalid fixture');await page.locator('#auto-login-start').click();
  await expect(page.locator('#auto-login-error')).toBeVisible();await expect(page.locator('#auto-login-input')).toHaveValue('invalid fixture');
  await page.locator('#auto-login-channel').selectOption('bps');
  await page.locator('#auto-login-input').fill('first@example.test----bad-fixture----\nsecond@example.test----correct-fixture----');
  await expect(page.locator('#auto-login-input')).toHaveClass(/login-secret/);
  await page.locator('#auto-login-reveal').check();await expect(page.locator('#auto-login-input')).not.toHaveClass(/login-secret/);await page.locator('#auto-login-reveal').uncheck();
  await fs.mkdir(path.join(root,'build/validation'),{recursive:true});
  await page.locator('#modal').screenshot({path:path.join(root,'build/validation/auto-login-entry.png')});
  await page.locator('#auto-login-start').click();await expect(page.locator('#auto-login-input')).toHaveValue('');
  await expect(page.locator('[data-auth-choice="fixture-team"]')).toBeVisible();
  await expect(page.locator('.login-queue-row').first()).toHaveAttribute('data-status','failed');
  await page.locator('[data-auth-choice="fixture-team"]').focus();await page.waitForTimeout(1000);await expect(page.locator('[data-auth-choice="fixture-team"]')).toBeFocused();
  await page.locator('#modal').screenshot({path:path.join(root,'build/validation/auto-login-workspace.png')});
  await page.locator('[data-auth-choice="fixture-team"]').click();
  await expect(page.locator('#auto-login-summary')).toContainText('1 / 2');await expect(page.locator('#auto-login-close')).toHaveText('完成');
  await expect(page.locator('[data-auth-use]')).toHaveCount(1);await expect(page.locator('#auto-login-open')).toBeHidden();
  await page.evaluate(()=>applyTheme('dark'));await page.waitForTimeout(250);await page.locator('#modal').screenshot({path:path.join(root,'build/validation/auto-login-result-dark.png')});
  console.log(JSON.stringify(await page.locator('#auto-login-close').evaluate(el=>({theme:document.documentElement.dataset.theme,button:getComputedStyle(el).backgroundColor,surface:getComputedStyle(el).getPropertyValue('--surface'),color:getComputedStyle(el).color}))));
  const result=await client.evaluate(()=>({closed:globalThis.autoFixture.closed,browserCalls:globalThis.autoFixture.browserCalls,approved:globalThis.autoFixture.approved,cancelled:globalThis.autoFixture.cancelled}));
  if(result.browserCalls||result.closed!==2||!result.approved)throw Error('Protocol lifecycle mismatch');
  const rendered=await page.locator('#modal').textContent();if(/bad-fixture|correct-fixture/.test(rendered))throw Error('Credential exposure in result');
  await page.locator('[data-auth-use]').click();await expect(page.locator('#modal')).not.toBeVisible();
  await expect(page.locator('#channel')).toHaveValue('bps');await expect(page.locator('.account-card')).toHaveCount(1);
  const snapshot=await page.evaluate(()=>window.bridge.snapshot());if(snapshot.accounts.some(a=>'access_token'in a||'refresh_token'in a||'id_token'in a))throw Error('Token exposed to renderer');
  await page.locator('#login-account').click();await page.locator('#auto-login-channel').selectOption('bps');await page.locator('#auto-login-input').fill('cancel@example.test----correct-fixture----');await page.locator('#auto-login-start').click();
  await expect(page.locator('[data-auth-choice="fixture-team"]')).toBeVisible();await page.keyboard.press('Escape');
  await expect.poll(()=>client.evaluate(()=>globalThis.autoFixture.closed)).toBe(3);
  if(errors.length)throw Error(errors.join('\n'));
  console.log(JSON.stringify({ok:true,synthetic:true,defaultProtocol:true,batchPartialSuccess:true,explicitWorkspace:true,maskedInput:true,clearedInput:true,selectSavedAccount:true,cancelCleanup:true,browserCalls:0,rendererErrors:0}));
 }finally{
  const {BridgeService}=require('../lib/service.cjs'),svc=new BridgeService({home,binary:''});svc.config=JSON.parse(await fs.readFile(path.join(home,'config.json'),'utf8'));await svc.stop();
  if(process.platform==='win32')require('node:child_process').execFileSync('taskkill.exe',['/PID',String(client.process().pid),'/T','/F'],{windowsHide:true,stdio:'ignore'});else client.process().kill('SIGKILL');
  await fs.rm(home,{recursive:true,force:true,maxRetries:8,retryDelay:250});
 }
})().catch(e=>{console.error(e);process.exitCode=1;});
