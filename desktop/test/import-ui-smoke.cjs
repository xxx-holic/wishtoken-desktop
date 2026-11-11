'use strict';
const { _electron:electron,expect }=require('@playwright/test');
const fs=require('node:fs/promises'),path=require('node:path'),os=require('node:os'),net=require('node:net');
const root=path.resolve(__dirname,'../..');
(async()=>{
 const home=await fs.mkdtemp(path.join(os.tmpdir(),'wishtoken-import-ui-'));
 const socket=net.createServer();await new Promise(r=>socket.listen(0,'127.0.0.1',r));const port=socket.address().port;await new Promise(r=>socket.close(r));
 await fs.writeFile(path.join(home,'config.json'),JSON.stringify({listen:`127.0.0.1:${port}`,api_key:'fixture-import-key',auto_refresh:false,usage_probe:false}));
 const jwt=n=>[Buffer.from(JSON.stringify({alg:'none'})).toString('base64url'),Buffer.from(JSON.stringify({email:`paste${n}@example.test`,sub:'fixture-user-'+n,exp:Date.parse('2099-10-31T06:48:37Z')/1000})).toString('base64url'),Buffer.from('synthetic-signature').toString('base64url')].join('.');
 const fixture=n=>({id_token:jwt(n),access_token:jwt(n),refresh_token:'synthetic-refresh-'+n,account_id:'fixture-workspace-'+n,last_refresh:'2026-10-21T07:21:33.000Z',email:`paste${n}@example.test`,type:'codex',expired:'2099-10-31T06:48:37.000Z'});
 const file=path.join(home,'fixture-import.json');await fs.writeFile(file,JSON.stringify(fixture(3)));
 const client=await electron.launch({executablePath:process.env.GPTBRIDGE_TEST_APP || require('electron'),args:process.env.GPTBRIDGE_TEST_APP?[]:[path.join(root,'desktop')],env:{...process.env,GPTBRIDGE_DESKTOP_HOME:home,GPTBRIDGE_MAIN_CODEX_HOME:path.join(home,'unused-main')}});
 try{
  const page=await client.firstWindow(),errors=[];page.on('pageerror',e=>errors.push(e.message));
  await expect(page.locator('#service-label')).toHaveText('本地服务运行中');
  await client.evaluate(({app,dialog},file)=>{
   const require=process.mainModule.require.bind(process.mainModule),{BridgeService}=require(require('node:path').join(app.getAppPath(),'lib/service.cjs'));
   const original=BridgeService.prototype.request;
   BridgeService.prototype.request=function(route,...args){if(/^\/api\/accounts\/[^/]+\/usage$/.test(route))return Promise.resolve({});return original.call(this,route,...args);};
   globalThis.fileDialogCalls=0;dialog.showOpenDialog=async()=>++globalThis.fileDialogCalls===1?{canceled:true,filePaths:[]}:{canceled:false,filePaths:[file]};
  },file);
  await page.locator('#import-files').click();await expect(page.locator('#json-text')).toBeFocused();await expect(page.locator('#submit-paste')).toBeDisabled();
  if(await client.evaluate(()=>globalThis.fileDialogCalls)!==0)throw new Error('Header opened file picker instead of paste dialog');
  const malformed='[{"access_token":"synthetic-sensitive-fragment",';
  await page.locator('#json-text').fill(malformed);await page.locator('#submit-paste').click();
  await expect(page.locator('#import-input-error')).toBeVisible();await expect(page.locator('#json-text')).toHaveValue(malformed);
  if((await page.locator('#import-input-error').innerText()).includes('synthetic-sensitive-fragment'))throw new Error('Input leaked in validation error');
  await page.locator('#json-text').fill(JSON.stringify([fixture(1),fixture(2)],null,2));
  await page.locator('#json-text').evaluate(el=>{el.scrollTop=0;});
  await fs.mkdir(path.join(root,'build/validation'),{recursive:true});await page.locator('#modal').screenshot({path:path.join(root,'build/validation/import-paste-ui.png')});
  await page.locator('#submit-paste').click();await expect(page.locator('#modal')).not.toBeVisible();await expect(page.locator('.account-card')).toHaveCount(2);
  const snapshot=await page.evaluate(()=>window.bridge.snapshot());
  if(snapshot.accounts.some(a=>'access_token' in a || 'refresh_token' in a || 'id_token' in a))throw new Error('Credential returned to renderer');
  const stored=JSON.parse(await fs.readFile(path.join(home,'accounts.json'),'utf8'));
  for(const a of stored.accounts){const n=a.email==='paste1@example.test'?1:2;const expected=fixture(n);if(a.account_id!==expected.account_id || a.id_token!==expected.id_token || a.access_token!==expected.access_token || a.refresh_token!==expected.refresh_token || Date.parse(a.expires_at)!==Date.parse(expected.expired) || Date.parse(a.last_refresh)!==Date.parse(expected.last_refresh))throw new Error('Pasted array fields were not preserved');}
  await page.locator('#import-files').click();await expect(page.locator('#json-text')).toHaveValue('');
  await page.locator('#json-text').fill(JSON.stringify(fixture(1)));await page.locator('#submit-paste').click();await expect(page.locator('#modal')).not.toBeVisible();await expect(page.locator('.account-card')).toHaveCount(2);
  await page.locator('#paste-json').click();await page.locator('#json-text').fill('unsaved fixture');await page.locator('#cancel-paste').click();await expect(page.locator('#json-text')).toHaveValue('');
  await page.locator('#import-files').click();await page.locator('#import-file-tab').click();await page.locator('#choose-import-files').click();
  await expect(page.locator('#modal')).toBeVisible();await expect(page.locator('#choose-import-files')).toBeEnabled();
  await page.locator('#choose-import-files').click();await expect(page.locator('#modal')).not.toBeVisible();await expect(page.locator('.account-card')).toHaveCount(3);
  if(errors.length)throw new Error(errors.join('\n'));
  console.log(JSON.stringify({ok:true,synthetic:true,headerPaste:true,arrayFieldsPreserved:true,duplicateMerged:true,fileImport:true,cancelPreserved:true,errorRedacted:true,rendererErrors:0}));
 }finally{
  const config=JSON.parse(await fs.readFile(path.join(home,'config.json'),'utf8'));const {BridgeService}=require('../lib/service.cjs'),svc=new BridgeService({home,binary:''});svc.config=config;await svc.stop();
  if(process.platform==='win32')require('node:child_process').execFileSync('taskkill.exe',['/PID',String(client.process().pid),'/T','/F'],{windowsHide:true,stdio:'ignore'});else client.process().kill('SIGKILL');
 }
})().catch(e=>{console.error(e);process.exitCode=1;});
