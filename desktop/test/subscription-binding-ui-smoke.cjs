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
  await page.evaluate(()=>window.bridge.importText(JSON.stringify([{type:'fenjue',token:'synthetic-fenjue-binding-ui',user_id:'fixture',email:'binding@example.test',name:'绑定测试账号'}])));
  await page.evaluate(()=>refresh());await page.locator('[data-page="settings"]').click();await expect(page.locator('#fenjue-account option')).toHaveCount(1);
  await app.evaluate(({app})=>{
   const require=process.mainModule.require.bind(process.mainModule),{SubscriptionBinding}=require(require('node:path').join(app.getAppPath(),'lib/subscription-binding.cjs'));
   SubscriptionBinding.prototype.start=async function(input){globalThis.bindingInput={account_id:input.account_id,mode:input.mode,proxy_url:input.proxy_url,text:input.text};return {id:'binding-fixture',mode:input.mode,account_id:input.account_id,done:false,rows:[{index:0,email:'binding@example.test',status:'queued',stage:'queued',message:'等待授权'}]};};
   SubscriptionBinding.prototype.snapshot=function(){return {id:'binding-fixture',mode:'protocol',account_id:'fixture',done:true,rows:[{index:0,email:'binding@example.test',status:'completed',stage:'completed',message:'协议订阅授权已完成，可点检查核对状态。',bound_email:'binding@example.test'}]};};
   SubscriptionBinding.prototype.cancel=async function(){globalThis.bindingCancelled=true;return {id:'binding-fixture',mode:'protocol',account_id:'fixture',done:true,rows:[]};};
  });
  await page.locator('#fenjue-bind').click();await page.locator('#binding-input').fill('binding@example.test----fixture-password----GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ');await page.locator('#binding-proxy').fill('http://127.0.0.1:7890');await page.locator('#binding-start').click();
  await expect(page.locator('#binding-summary')).toHaveText('绑定完成',{timeout:5000});
  const observed=await app.evaluate(()=>globalThis.bindingInput);if(!observed||observed.mode!=='protocol'||!observed.account_id||observed.proxy_url!=='http://127.0.0.1:7890'||!observed.text.includes('binding@example.test'))throw Error('binding input was not forwarded');
  await fs.mkdir(path.join(root,'build/validation'),{recursive:true});await page.screenshot({path:path.join(root,'build/validation/subscription-binding-ui.png'),fullPage:true});
  if(errors.length)throw Error(errors.join('\n'));
  console.log(JSON.stringify({passed:true,synthetic:true,protocolDefault:true,proxyForwarded:true,rendererErrors:0}));
 }finally{
  const {BridgeService}=require('../lib/service.cjs');const svc=new BridgeService({home,binary:''});svc.config=JSON.parse(await fs.readFile(path.join(home,'config.json'),'utf8'));await svc.stop();
  if(process.platform==='win32')require('node:child_process').execFileSync('taskkill.exe',['/PID',String(app.process().pid),'/T','/F'],{windowsHide:true,stdio:'ignore'});else app.process().kill('SIGKILL');
 }
})().catch(e=>{console.error(e.message);process.exitCode=1});
