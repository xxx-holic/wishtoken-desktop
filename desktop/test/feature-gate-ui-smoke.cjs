'use strict';
const {_electron:electron,expect}=require('@playwright/test');
const fs=require('node:fs/promises'),path=require('node:path'),os=require('node:os'),net=require('node:net');
const {issueFixture}=require('./feature-license-fixture.cjs');
const {CANDY_PROMPT}=require('../lib/benchmarks.cjs');
const root=path.resolve(__dirname,'../..');
(async()=>{
 const home=await fs.mkdtemp(path.join(os.tmpdir(),'wishtoken-license-ui-'));
 const sock=net.createServer();await new Promise(r=>sock.listen(0,'127.0.0.1',r));const port=sock.address().port;await new Promise(r=>sock.close(r));
 await fs.writeFile(path.join(home,'config.json'),JSON.stringify({listen:`127.0.0.1:${port}`,api_key:'fixture-license-key',auto_refresh:false,usage_probe:false}));
 const app=await electron.launch({executablePath:process.env.GPTBRIDGE_TEST_APP||require('electron'),args:process.env.GPTBRIDGE_TEST_APP?[]:[path.join(root,'desktop')],env:{...process.env,GPTBRIDGE_DESKTOP_HOME:home,GPTBRIDGE_MAIN_CODEX_HOME:path.join(home,'unused-main')}});
 try{
  const page=await app.firstWindow(),errors=[];page.on('pageerror',e=>errors.push(e.message));
  await expect(page.locator('#service-label')).toHaveText('本地服务运行中');
  await page.locator('[data-page="settings"]').click();await expect(page.locator('#fenjue-login')).toBeDisabled();
  const blocked=await page.evaluate(async()=>{try{await window.bridge.subscriptionBindingStart({});return false}catch(e){return e.message.includes('锁定')}});if(!blocked)throw Error('Binding IPC bypass');
  for(const code of ['WTK1.abc.def',await issueFixture(home,{installation:'WT-'+'A'.repeat(32)}),await issueFixture(home,{days:-1})]){
   await page.locator('#feature-code').fill(code);await page.locator('#feature-activate').click();await expect(page.locator('#feature-error')).toBeVisible();await expect(page.locator('#fenjue-login')).toBeDisabled();
  }
  const valid=await issueFixture(home);await page.locator('#feature-code').fill(valid);await page.locator('#feature-activate').click();await expect(page.locator('#feature-license-status')).toContainText('已解锁');await expect(page.locator('#fenjue-login')).toBeEnabled();await expect(page.locator('#feature-code')).toHaveValue('');
  await page.evaluate(()=>window.bridge.importText(JSON.stringify([{type:'fenjue',token:'synthetic-feature-license-fixture',user_id:'fixture-fenjue',email:'feature@example.test'}])));
  await page.locator('#feature-deactivate').click();await expect(page.locator('#fenjue-login')).toBeDisabled();
  const snapshot=await page.evaluate(()=>window.bridge.snapshot());if(snapshot.accounts.length!==1||snapshot.features.fenjue.unlocked)throw Error('Relock lost account or failed');
  await page.locator('#feature-code').fill('');
  const screenshots=path.join(root,'build/validation/preview6');await fs.mkdir(screenshots,{recursive:true});
  await page.locator('#fenjue-license-settings').screenshot({path:path.join(screenshots,'feature-locked.png')});
  await page.locator('#bps-guide').screenshot({path:path.join(screenshots,'bps-guide.png')});
  await app.evaluate(({app,shell})=>{
   const require=process.mainModule.require.bind(process.mainModule),{BridgeService}=require(require('node:path').join(app.getAppPath(),'lib/service.cjs')),old=BridgeService.prototype.request;
   globalThis.bpsUIFixture={ready:false,id:'',generate:0};shell.openExternal=async()=>{};
   BridgeService.prototype.request=async function(route,method,body,...rest){
    const f=globalThis.bpsUIFixture;
    if(route==='/api/login/start'){f.expected=body.expected_email;return {channel:'bps',session:'123',user_code:'DEMO12345',auth_url:'https://auth.openai.com/api/accounts/deviceauth/authorize?client_id=app_fnr0pYvVwwFDocDumLG3H2Bp'};}
    if(route.startsWith('/api/login/status'))return {channel:'bps',session:'123',active:!f.ready,done:f.ready,account_id:f.ready?f.id:undefined,user_code:'DEMO12345',auth_url:'https://auth.openai.com/api/accounts/deviceauth/authorize?client_id=app_fnr0pYvVwwFDocDumLG3H2Bp'};
    if(route.startsWith('/api/login/cancel'))return {ok:true};
    if(route==='/api/pelican/generate'){f.generate++;throw Error('Test must only prepare the benchmark');}
    return old.call(this,route,method,body,...rest);
   };
  });
  const token=[Buffer.from('{"alg":"none"}').toString('base64url'),Buffer.from(JSON.stringify({exp:4102444800,email:'bps@example.test',sub:'fixture-user',client_id:'app_fnr0pYvVwwFDocDumLG3H2Bp','https://api.openai.com/auth':{chatgpt_account_id:'fixture-bps',chatgpt_user_id:'fixture-user',chatgpt_plan_type:'pro'}})).toString('base64url'),'fixture'].join('.');
  const imported=await page.evaluate(token=>window.bridge.importText(JSON.stringify({type:'codex',access_token:token})),token);const id=imported.ids[0];
  await page.evaluate(id=>window.bridge.account({id,action:'update',disabled:true}),id);await page.evaluate(()=>refresh());await app.evaluate((_,id)=>{globalThis.bpsUIFixture.id=id;},id);
  await page.locator('#bps-guide-login').click();await page.locator('#login-expected-email').fill('bps@example.test');await page.locator('#login-bps').click();await expect(page.locator('#copy-login-code')).toBeVisible();
  if(await app.evaluate(()=>globalThis.bpsUIFixture.expected)!=='bps@example.test')throw Error('Email not pinned');
  await app.evaluate(()=>{globalThis.bpsUIFixture.ready=true;});await expect(page.locator('#bps-saved-test')).toContainText('启用');
  await page.locator('#modal').screenshot({path:path.join(screenshots,'bps-paused-success.png')});
  await page.locator('#bps-saved-test').click();await expect(page.locator('#pelican-channel')).toHaveValue('bps');await expect(page.locator('#pelican-prompt')).toHaveValue(CANDY_PROMPT);await expect(page.locator('#pelican-effort')).toHaveValue('medium');
  const end=await page.evaluate(()=>window.bridge.snapshot());if(end.accounts.find(a=>a.id===id).disabled)throw Error('Explicit enable not applied');if(await app.evaluate(()=>globalThis.bpsUIFixture.generate)!==0)throw Error('Unrequested inference');
  await page.screenshot({path:path.join(screenshots,'bps-test-ready.png'),fullPage:true});
  if(errors.length)throw Error(errors.join('\n'));
  console.log(JSON.stringify({passed:true,synthetic:true,lockedIPC:true,invalidExpiredCrossInstallRejected:true,signedUnlock:true,relockPreservesAccount:true,bpsIdentityPinned:true,bpsPausedPreserved:true,explicitEnableAndPrepare:true,automaticInference:0,rendererErrors:0}));
 }finally{
  const {BridgeService}=require('../lib/service.cjs');const svc=new BridgeService({home});svc.config=JSON.parse(await fs.readFile(path.join(home,'config.json'),'utf8'));await svc.stop();
  require('./feature-license-fixture.cjs').stopFixtureProcess(app);
  const resolved=path.resolve(home);if(resolved.startsWith(path.resolve(os.tmpdir())+path.sep)&&path.basename(resolved).startsWith('wishtoken-license-ui-'))await fs.rm(resolved,{recursive:true,force:true,maxRetries:3});
 }
})().catch(e=>{console.error(e.message);process.exitCode=1});
