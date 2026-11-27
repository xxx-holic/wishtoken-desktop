'use strict';
const {_electron:electron,expect}=require('@playwright/test');
const fs=require('node:fs/promises'),path=require('node:path'),os=require('node:os'),net=require('node:net');
const root=path.resolve(__dirname,'../..');
(async()=>{
 const home=await fs.mkdtemp(path.join(os.tmpdir(),'wishtoken-binding-ui-'));
 const sock=net.createServer();await new Promise(r=>sock.listen(0,'127.0.0.1',r));const port=sock.address().port;await new Promise(r=>sock.close(r));
 await fs.writeFile(path.join(home,'config.json'),JSON.stringify({listen:`127.0.0.1:${port}`,api_key:'fixture-ui-key',auto_refresh:false,usage_probe:false}));
 const app=await electron.launch({executablePath:process.env.GPTBRIDGE_TEST_APP||require('electron'),args:process.env.GPTBRIDGE_TEST_APP?[]:[path.join(root,'desktop')],env:{...process.env,GPTBRIDGE_DESKTOP_HOME:home,GPTBRIDGE_MAIN_CODEX_HOME:path.join(home,'unused-main')}});
 try{
  const page=await app.firstWindow(),errors=[];page.on('pageerror',e=>errors.push(e.message));
  await expect(page.locator('#service-label')).toHaveText('本地服务运行中');
  await require('./feature-license-fixture.cjs').unlockFixture(home);await page.evaluate(()=>refresh());
  await page.evaluate(()=>window.bridge.importText(JSON.stringify([{type:'fenjue',token:'synthetic-fenjue-binding-ui',user_id:'fixture',email:'binding@example.test',name:'绑定测试账号'}])));
  await page.evaluate(()=>refresh());await page.locator('[data-page="settings"]').click();await expect(page.locator('#fenjue-account option')).toHaveCount(1);
  await app.evaluate(({app})=>{
   const require=process.mainModule.require.bind(process.mainModule),{SubscriptionBinding}=require(require('node:path').join(app.getAppPath(),'lib/subscription-binding.cjs'));
   const {BridgeService}=require(require('node:path').join(app.getAppPath(),'lib/service.cjs'));
   const start=SubscriptionBinding.prototype.start,request=BridgeService.prototype.request;
   globalThis.bindingFixtureEmail='different@example.test';globalThis.bindingChecks=0;
   BridgeService.prototype.request=async function(route,...args){
    if(route.startsWith('/api/fenjue/subscription?')){globalThis.bindingChecks++;return {state:'connected',email:globalThis.bindingFixtureEmail};}
    return request.call(this,route,...args);
   };
   SubscriptionBinding.prototype.start=async function(input){
    globalThis.bindingInput={account_id:input.account_id,mode:input.mode,proxy_url:input.proxy_url,text:input.text};
    this.httpFactory=async()=>{throw Error('preflight must prevent another login');};
    this.browserFactory=async()=>{throw Error('preflight must prevent browser');};
    return start.call(this,input);
   };
  });
  await page.locator('#fenjue-bind').click();await expect(page.locator('#binding-proxy')).toHaveValue('');
  await page.locator('#binding-input').fill('binding@example.test----fixture-password----GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ');await page.locator('#binding-start').click();
  await expect(page.locator('#binding-summary')).toHaveText('绑定未完成',{timeout:5000});await expect(page.locator('#binding-input')).toHaveValue('');
  await expect(page.locator('#binding-rows')).toContainText('授权身份与输入账号不一致');await page.locator('#binding-close').click();
  await app.evaluate(()=>{globalThis.bindingFixtureEmail='binding@example.test';});
  await page.locator('#fenjue-bind').click();await page.locator('#binding-input').fill('binding@example.test----fixture-password----GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ');await page.locator('#binding-proxy').fill('http://127.0.0.1:7890');await page.locator('#binding-start').click();
  await expect(page.locator('#binding-summary')).toHaveText('绑定完成',{timeout:5000});
  await expect(page.locator('#fenjue-status')).toContainText('已回查绑定 binding@example.test');
  await expect(page.locator('#binding-input')).toHaveValue('');
  const observed=await app.evaluate(()=>globalThis.bindingInput);if(!observed||observed.mode!=='protocol'||!observed.account_id||observed.proxy_url!=='http://127.0.0.1:7890'||!observed.text.includes('binding@example.test'))throw Error('binding input was not forwarded');
  await fs.mkdir(path.join(root,'build/validation'),{recursive:true});await page.screenshot({path:path.join(root,'build/validation/subscription-binding-ui.png'),fullPage:true});
  if(errors.length)throw Error(errors.join('\n'));
  if(await app.evaluate(()=>globalThis.bindingChecks)!==2)throw Error('unexpected number of subscription checks');
  console.log(JSON.stringify({passed:true,synthetic:true,realBindingManager:true,identityMismatchRejected:true,verifiedBinding:true,protocolDefault:true,proxyForwarded:true,rendererErrors:0}));
 }finally{
  const {BridgeService}=require('../lib/service.cjs');const svc=new BridgeService({home,binary:''});svc.config=JSON.parse(await fs.readFile(path.join(home,'config.json'),'utf8'));await svc.stop();
  require('./feature-license-fixture.cjs').stopFixtureProcess(app);
  const resolved=path.resolve(home),base=path.resolve(os.tmpdir())+path.sep;
  if(resolved.startsWith(base)&&path.basename(resolved).startsWith('wishtoken-binding-ui-'))await fs.rm(resolved,{recursive:true,force:true,maxRetries:3});
 }
})().catch(e=>{console.error(e.message);process.exitCode=1});
