'use strict';
const { _electron:electron,expect }=require('@playwright/test');
const fs=require('node:fs/promises'),path=require('node:path'),os=require('node:os'),net=require('node:net');
const { CANDY_PROMPT }=require('../lib/benchmarks.cjs');
const root=path.resolve(__dirname,'../..');
(async()=>{
 const home=await fs.mkdtemp(path.join(os.tmpdir(),'wishtoken-work-ui-'));
 const socket=net.createServer();await new Promise(r=>socket.listen(0,'127.0.0.1',r));const port=socket.address().port;await new Promise(r=>socket.close(r));
 await fs.writeFile(path.join(home,'config.json'),JSON.stringify({listen:`127.0.0.1:${port}`,api_key:'fixture-ui-key',auto_refresh:false,usage_probe:false}));
 const client=await electron.launch({executablePath:process.env.GPTBRIDGE_TEST_APP || require('electron'),args:process.env.GPTBRIDGE_TEST_APP?[]:[path.join(root,'desktop')],env:{...process.env,GPTBRIDGE_DESKTOP_HOME:home,GPTBRIDGE_MAIN_CODEX_HOME:path.join(home,'unused-main')}});
 try {
  const page=await client.firstWindow();const errors=[];page.on('pageerror',e=>errors.push(e.message));
  await expect(page.locator('#service-label')).toHaveText('本地服务运行中');
  await page.locator('[data-page="settings"]').click();
  await expect(page.locator('#bps-transport')).toHaveValue('http');
  await page.locator('#bps-transport').selectOption('websocket');
  await page.locator('#save-settings').click();
  await expect.poll(async()=> (await page.evaluate(()=>window.bridge.snapshot())).settings.bps_transport).toBe('websocket');
  await page.locator('#bps-transport').selectOption('http');await page.locator('#save-settings').click();
  await expect.poll(async()=> (await page.evaluate(()=>window.bridge.snapshot())).settings.bps_transport).toBe('http');
  await page.locator('[data-page="accounts"]').click();
  await client.evaluate(({app})=>{
   const require=process.mainModule.require.bind(process.mainModule),{Work}=require(require('node:path').join(app.getAppPath(),'lib/work.cjs'));
   if(typeof Work.prototype.chromium().connectOverCDP!=='function')throw new Error('Packaged Work browser runtime missing');
  });
  await page.evaluate(()=>window.bridge.importText(JSON.stringify({email:'work-fixture@example.test',account_id:'workspace-fixture',access_token:'synthetic-fixture-never-sent',name:'Work 测试账号'})));
  await page.evaluate(()=>refresh());
  await page.locator('#channel').selectOption('work');
  await expect(page.locator('#model option')).toHaveCount(7);await expect(page.locator('#model')).toHaveValue('gpt-6.1-sol-wm');await expect(page.locator('[data-effort="xhigh"]')).toHaveClass(/chosen/);
  await expect(page.locator('[data-effort="high"]')).toBeEnabled();await expect(page.locator('[data-effort="max"]')).toBeEnabled();await expect(page.locator('#speed')).toBeDisabled();
  await page.locator('#channel').selectOption('prism');
  await expect(page.locator('#prism-setup')).toBeVisible();await expect(page.locator('#model option')).toHaveCount(4);await expect(page.locator('#app-mode')).toHaveValue('isolated');
  await expect(page.locator('[data-effort="ultra"]')).toBeEnabled();await expect(page.locator('[data-effort="max"]')).toBeEnabled();
  await page.locator('#channel').selectOption('work');
  await expect(page.locator('#prism-setup')).toBeHidden();await expect(page.locator('#model')).toHaveValue('gpt-6.1-sol-wm');
  await page.locator('#work-setup').click();await expect(page.locator('#work-account option')).toHaveCount(1);await expect(page.locator('#work-open')).toBeEnabled();
  await client.evaluate(({app})=>{
   const require=process.mainModule.require.bind(process.mainModule),{BridgeService}=require(require('node:path').join(app.getAppPath(),'lib/service.cjs'));
   const original=BridgeService.prototype.request;
   BridgeService.prototype.request=async function(route,...args){
    if(route!=='/api/pelican/generate')return original.call(this,route,...args);
    globalThis.seenBenchmark=args[1];
    return {text:'先按形状选择。最终答案：21 颗。',response_model:'gpt-6-astra-wm',route:'work',duration_ms:1234,usage:null};
   };
  });
  await page.locator('[data-page="pelican"]').click();await page.locator('#pelican-channel').selectOption('work');await page.locator('#pelican-benchmark').selectOption('candy');
  await expect(page.locator('#pelican-prompt')).toHaveValue(CANDY_PROMPT);await expect(page.locator('#pelican-effort')).toHaveValue('medium');await expect(page.locator('#pelican-concurrency')).toHaveValue('1');
  await page.locator('#pelican-all').click();await page.locator('#pelican-start').click();
  await expect(page.locator('.candy-verdict strong')).toHaveText('本题通过',{timeout:15000});await expect(page.locator('.candy-answer')).toContainText('最终答案：21');
  await expect(page.locator('.pelican-result iframe')).toHaveCount(0);
  const sent=await client.evaluate(()=>globalThis.seenBenchmark);if(sent.prompt!==CANDY_PROMPT || sent.instructions || sent.expected_answer)throw new Error('Benchmark changed');
  await fs.mkdir(path.join(root,'build/validation'),{recursive:true});await page.screenshot({path:path.join(root,'build/validation/work-candy-ui.png'),fullPage:true});
  await page.locator('[data-pelican="source"]').click();await expect(page.locator('#pelican-source')).toHaveValue('先按形状选择。最终答案：21 颗。');
  if(errors.length)throw new Error(errors.join('\n'));
  console.log(JSON.stringify({ok:true,synthetic:true,workPicker:true,multiEffort:true,candyVerbatim:true,candyVerdict:true,rawAnswer:true,noHtmlPreview:true,rendererErrors:0}));
 } finally {
  const config=JSON.parse(await fs.readFile(path.join(home,'config.json'),'utf8'));const {BridgeService}=require('../lib/service.cjs');const svc=new BridgeService({home,binary:''});svc.config=config;await svc.stop();
  if(process.platform==='win32')require('node:child_process').execFileSync('taskkill.exe',['/PID',String(client.process().pid),'/T','/F'],{windowsHide:true,stdio:'ignore'});else client.process().kill('SIGKILL');
 }
})().catch(e=>{console.error(e);process.exitCode=1;});
