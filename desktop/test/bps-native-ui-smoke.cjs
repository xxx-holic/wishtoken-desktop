'use strict';
const {_electron:electron,expect}=require('@playwright/test');
const fs=require('node:fs/promises'),path=require('node:path'),os=require('node:os'),net=require('node:net');
const {BridgeService}=require('../lib/service.cjs');
const root=path.resolve(__dirname,'../..');
(async()=>{
 const home=await fs.mkdtemp(path.join(os.tmpdir(),'wishtoken-primer-ui-'));
 const socket=net.createServer();await new Promise(r=>socket.listen(0,'127.0.0.1',r));const port=socket.address().port;await new Promise(r=>socket.close(r));
 await fs.writeFile(path.join(home,'config.json'),JSON.stringify({listen:`127.0.0.1:${port}`,api_key:'primer-ui-key',auto_refresh:false,usage_probe:false}));
 const app=await electron.launch({executablePath:process.env.GPTBRIDGE_TEST_APP||require('electron'),args:process.env.GPTBRIDGE_TEST_APP?[]:[path.join(root,'desktop')],env:{...process.env,GPTBRIDGE_DESKTOP_HOME:home,GPTBRIDGE_MAIN_CODEX_HOME:path.join(home,'unused-main')}});
 try{
  const page=await app.firstWindow(),errors=[];page.on('pageerror',e=>errors.push(e.message));
  await expect(page.locator('#service-label')).toHaveText('本地服务运行中');
  const token=client=>['eyJhbGciOiJub25lIn0',Buffer.from(JSON.stringify({client_id:client,exp:4102444800,email:'primer@example.test','https://api.openai.com/auth':{chatgpt_account_id:'workspace-primer',chatgpt_user_id:'same-user',chatgpt_plan_type:'pro'}})).toString('base64url'),'synthetic'].join('.');
  const imported=await page.evaluate(text=>window.bridge.importText(text),JSON.stringify(['app_EMoamEEZ73f0CkXaXp7hrann','app_fnr0pYvVwwFDocDumLG3H2Bp'].map(client=>({access_token:token(client)}))));
  await page.evaluate(()=>refresh());
  const accounts=(await page.evaluate(()=>window.bridge.snapshot())).accounts;
  const native=accounts.find(a=>a.oauth_client_id==='app_EMoamEEZ73f0CkXaXp7hrann'),bps=accounts.find(a=>a.oauth_client_id==='app_fnr0pYvVwwFDocDumLG3H2Bp');
  if(imported.ids.length!==2||!native||!bps)throw Error('separate grants missing');
  await page.locator('#quick-account').selectOption(native.id);
  await page.locator('#channel').selectOption('bps_native');await expect(page.locator('#model option')).toHaveCount(5);
  await page.locator('#speed').selectOption('fast');await expect(page.locator('#speed')).toHaveValue('fast');
  await page.locator('#bps-native-setup').click();await expect(page.locator('#primer-bps option')).toHaveCount(1);
  await expect(page.locator('#primer-total')).toHaveValue('40');await expect(page.locator('#primer-state')).toHaveText('尚未预热');
  await expect(page.locator('#primer-use')).toBeHidden();
  const svc=new BridgeService({home,binary:''});svc.config=JSON.parse(await fs.readFile(path.join(home,'config.json'),'utf8'));
  let blocked=false;try{await svc.request('/api/codex/launch','POST',{account_id:native.id,channel:'bps_native',model:'gpt-6-astra',effort:'medium',directory:home,prepare_only:true});}catch(e){blocked=/预热/.test(e.message)}
  if(!blocked)throw Error('real backend did not gate premature launch');
  // UI lifecycle uses explicit synthetic receipts; backend HTTP sequencing is
  // exercised independently in internal/server/bps_native_test.go.
  await app.evaluate(({app})=>{
   const req=process.mainModule.require.bind(process.mainModule),{BridgeService}=req(req('node:path').join(app.getAppPath(),'lib/service.cjs'));
   const original=BridgeService.prototype.request;
   BridgeService.prototype.request=async function(route,...args){
    if(route==='/api/bps-native/start'){globalThis.primerInput=args[1];globalThis.primerFixture={account_id:args[1].account_id,bps_account_id:args[1].bps_account_id,state:'running',requested:args[1].total_requests,plan:['gpt-6-astra','gpt-6.1-sol'],completed:1,current_model:'gpt-6.1-sol'};return globalThis.primerFixture;}
    if(route==='/api/bps-native/cancel'){globalThis.primerFixture={...globalThis.primerFixture,state:'cancelled',error:'已停止'};return {ok:true};}
    const result=await original.call(this,route,...args);
    if(route.startsWith('/api/bps-native/status')&&globalThis.primerFixture)return {...result,job:globalThis.primerFixture};
    return result;
   };
  });
  await page.locator('#primer-total').fill('2');await page.locator('#primer-start').click();await expect(page.locator('#primer-state')).toHaveText('BPS 预热中');
  await expect(page.locator('#primer-use')).toBeHidden();await page.locator('#primer-stop').click();await expect(page.locator('#primer-state')).toHaveText('已停止',{timeout:5000});
  await expect(page.locator('#primer-start')).toHaveText('继续未完成部分');
  await page.locator('#primer-start').click();
  await app.evaluate(()=>{globalThis.primerFixture={...globalThis.primerFixture,state:'failed',error:'BPS 限流，已暂停',retry_at:new Date(Date.now()+120000).toISOString()};});
  await expect(page.locator('#primer-error')).toContainText('下次可尝试时间',{timeout:5000});await expect(page.locator('#primer-use')).toBeHidden();
  await expect(page.locator('#primer-start')).toBeDisabled();await expect(page.locator('#primer-restart')).toBeDisabled();
  await expect(page.locator('#primer-count')).toHaveText('1 / 2');
  await expect(page.locator('#primer-models li')).toHaveCount(2);
  await app.evaluate(()=>{globalThis.primerFixture={...globalThis.primerFixture,state:'ready',completed:2,error:'',retry_at:''};});
  await expect(page.locator('#primer-use')).toBeVisible({timeout:5000});
  await fs.mkdir(path.join(root,'build/validation'),{recursive:true});await page.screenshot({path:path.join(root,'build/validation/bps-native-ready.png'),fullPage:true});
  await page.locator('#primer-use').click();await expect(page.locator('#channel')).toHaveValue('bps_native');
  await expect(page.locator('#bps-native-summary')).toContainText('预热完成',{timeout:5000});
  await page.locator('#grant-filter').selectOption('bps');await expect(page.locator('.account-card')).toHaveCount(1);
  await expect(page.locator('.card-health')).toContainText('BPS 授权');
  await expect(page.locator('#selected-summary')).toContainText('Codex 授权');
  await page.locator('#search').fill('missing-account');await expect(page.locator('#account-result-count')).toHaveText('显示 0 / 2 条授权记录');
  await page.locator('#clear-account-filters').click();await expect(page.locator('.account-card')).toHaveCount(2);await expect(page.locator('#search')).toBeFocused();
  await page.locator('[data-page="pelican"]').click();await page.locator('#pelican-channel').selectOption('bps_native');await expect(page.locator('#pelican-model option')).toHaveCount(5);
  const sent=await app.evaluate(()=>globalThis.primerInput);if(sent.account_id!==native.id||sent.bps_account_id!==bps.id||sent.total_requests!==2)throw Error('identity or count changed');
  if(errors.length)throw Error(errors.join('\n'));
  console.log(JSON.stringify({passed:true,synthetic:true,realInitialStatus:true,realLaunchGate:true,cancel:true,resume:true,cooldown:true,nativeCatalog:true,rendererErrors:0}));
 }finally{
  const svc=new BridgeService({home,binary:''});svc.config=JSON.parse(await fs.readFile(path.join(home,'config.json'),'utf8'));await svc.stop();
  try{require('node:child_process').execFileSync('taskkill.exe',['/PID',String(app.process().pid),'/T','/F'],{windowsHide:true,stdio:'ignore'});}catch{}
  const resolved=path.resolve(home);if(resolved.startsWith(path.resolve(os.tmpdir())+path.sep)&&path.basename(resolved).startsWith('wishtoken-primer-ui-'))await fs.rm(resolved,{recursive:true,force:true,maxRetries:3});
 }
})().catch(error=>{console.error(error.message);process.exitCode=1});
