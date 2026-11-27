'use strict';
const {_electron:electron,expect}=require('@playwright/test');
const fs=require('node:fs/promises'),path=require('node:path'),os=require('node:os'),net=require('node:net');
const {CANDY_PROMPT}=require('../lib/benchmarks.cjs');
const root=path.resolve(__dirname,'../..');
(async()=>{
 const home=await fs.mkdtemp(path.join(os.tmpdir(),'wishtoken-fenjue-ui-'));
 const sock=net.createServer();await new Promise(r=>sock.listen(0,'127.0.0.1',r));const port=sock.address().port;await new Promise(r=>sock.close(r));
 await fs.writeFile(path.join(home,'config.json'),JSON.stringify({listen:`127.0.0.1:${port}`,api_key:'fixture-ui-key',auto_refresh:false,usage_probe:false}));
 const app=await electron.launch({executablePath:process.env.GPTBRIDGE_TEST_APP||require('electron'),args:process.env.GPTBRIDGE_TEST_APP?[]:[path.join(root,'desktop')],env:{...process.env,GPTBRIDGE_DESKTOP_HOME:home,GPTBRIDGE_MAIN_CODEX_HOME:path.join(home,'unused-main')}});
 try{
  const page=await app.firstWindow(),errors=[];page.on('pageerror',e=>errors.push(e.message));
  await expect(page.locator('#service-label')).toHaveText('本地服务运行中');
  await require('./feature-license-fixture.cjs').unlockFixture(home);await page.evaluate(()=>refresh());
  await page.evaluate(()=>window.bridge.importText(JSON.stringify([{type:'fenjue',token:'synthetic-fenjue-ui-credential',user_id:'fenjue-fixture',email:'fenjue@example.test',name:'焚诀测试账号'},{type:'codex',access_token:'synthetic-native-ui-credential',account_id:'native-fixture',email:'native@example.test'}])));
  await page.evaluate(()=>refresh());
  await page.locator('#channel').selectOption('fenjue');await expect(page.locator('#model option')).toHaveCount(3);
  await expect(page.locator('#speed')).toBeDisabled();await expect(page.locator('#app-mode')).toHaveValue('isolated');await expect(page.locator('#app-mode')).toBeDisabled();
  await expect(page.locator('#fenjue-setup')).toBeVisible();
  await app.evaluate(({app})=>{
   const require=process.mainModule.require.bind(process.mainModule),{BridgeService}=require(require('node:path').join(app.getAppPath(),'lib/service.cjs'));
   const old=BridgeService.prototype.request;
   BridgeService.prototype.request=async function(route,...args){
    if(route==='/api/pelican/generate'){globalThis.fenjueBenchmark=args[1];return {text:'最少取出21个糖果。至少需要摸出9个圆形和12个五角星形。',route:'fenjue',response_model:'openai/gpt-6-astra',duration_ms:1234,usage:{input_tokens:183,output_tokens:30,is_byok:true,cost:0}}}
    if(route.startsWith('/api/fenjue/subscription'))return globalThis.fixtureCooldown?{state:'error',reason:'subscription_cooldown'}:{state:'connected',email:'linked@example.test'};
    if(route==='/api/fenjue/login/start')return {session:'fenjue-acc-fixture',active:true,channel:'fenjue',user_code:'DEMO-1234',auth_url:Buffer.from("aHR0cHM6Ly9hcHAua2lsby5haS9kZXZpY2UtYXV0aD9jb2RlPURFTU8tMTIzNA==", "base64").toString("utf8")};
    if(route.startsWith('/api/fenjue/login/status'))return {session:'fenjue-acc-fixture',active:true,done:false,channel:'fenjue',user_code:'DEMO-1234',auth_url:Buffer.from("aHR0cHM6Ly9hcHAua2lsby5haS9kZXZpY2UtYXV0aD9jb2RlPURFTU8tMTIzNA==", "base64").toString("utf8")};
    if(route.startsWith('/api/fenjue/login/cancel')){globalThis.fenjueCancelled=true;return {ok:true}}
    return old.call(this,route,...args);
   };
  });
  await page.locator('#fenjue-setup').click();await expect(page.locator('#fenjue-account option')).toHaveCount(1);await page.locator('#fenjue-check').click();await expect(page.locator('#fenjue-status')).toContainText('linked@example.test');
  await app.evaluate(()=>{globalThis.fixtureCooldown=true;});await page.locator('#fenjue-check').click();await expect(page.locator('#fenjue-status')).toContainText('24 小时后复查');await expect(page.locator('#fenjue-status')).toContainText('无需重新绑定');
  await app.evaluate(()=>{globalThis.fixtureCooldown=false;});
  await page.locator('#fenjue-login').click();await expect(page.locator('#fenjue-code')).toHaveValue('DEMO-1234');await page.keyboard.press('Escape');
  await expect.poll(()=>app.evaluate(()=>globalThis.fenjueCancelled)).toBe(true);
  await fs.mkdir(path.join(root,'build/validation'),{recursive:true});await page.screenshot({path:path.join(root,'build/validation/fenjue-settings.png'),fullPage:true});
  await page.locator('[data-page="pelican"]').click();await page.locator('#pelican-channel').selectOption('fenjue');await page.locator('#pelican-benchmark').selectOption('candy');
  await expect(page.locator('#pelican-accounts input')).toHaveCount(1);await expect(page.locator('#pelican-prompt')).toHaveValue(CANDY_PROMPT);
  await page.locator('#pelican-all').click();await page.locator('#pelican-start').click();await expect(page.locator('.candy-verdict strong')).toHaveText('本题通过',{timeout:15000});
  const sent=await app.evaluate(()=>globalThis.fenjueBenchmark);if(sent.channel!=='fenjue'||sent.prompt!==CANDY_PROMPT||sent.instructions)throw Error('benchmark changed');
  await page.screenshot({path:path.join(root,'build/validation/fenjue-candy.png'),fullPage:true});
  if(errors.length)throw Error(errors.join('\n'));
  console.log(JSON.stringify({passed:true,synthetic:true,catalog:true,isolatedProfile:true,deviceCancel:true,subscription:true,candyVerbatim:true,rendererErrors:0}));
 }finally{
  const {BridgeService}=require('../lib/service.cjs');const svc=new BridgeService({home,binary:''});svc.config=JSON.parse(await fs.readFile(path.join(home,'config.json'),'utf8'));await svc.stop();
  require('./feature-license-fixture.cjs').stopFixtureProcess(app);
  const resolved=path.resolve(home),base=path.resolve(os.tmpdir())+path.sep;
  if(resolved.startsWith(base)&&path.basename(resolved).startsWith('wishtoken-fenjue-ui-'))await fs.rm(resolved,{recursive:true,force:true,maxRetries:3});
 }
})().catch(e=>{console.error(e.message);process.exitCode=1});
