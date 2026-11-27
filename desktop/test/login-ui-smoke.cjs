'use strict';
const {_electron:electron,expect}=require('@playwright/test');
const fs=require('node:fs/promises'),path=require('node:path'),os=require('node:os'),net=require('node:net');
const root=path.resolve(__dirname,'../..');
(async()=>{
 const home=await fs.mkdtemp(path.join(os.tmpdir(),'wishtoken-login-ui-'));
 const socket=net.createServer();await new Promise(r=>socket.listen(0,'127.0.0.1',r));const port=socket.address().port;await new Promise(r=>socket.close(r));
 await fs.writeFile(path.join(home,'config.json'),JSON.stringify({listen:`127.0.0.1:${port}`,api_key:'fixture-login-key',auto_refresh:false,usage_probe:false}));
 const client=await electron.launch({executablePath:process.env.GPTBRIDGE_TEST_APP||require('electron'),args:process.env.GPTBRIDGE_TEST_APP?[]:[path.join(root,'desktop')],env:{...process.env,GPTBRIDGE_DESKTOP_HOME:home,GPTBRIDGE_MAIN_CODEX_HOME:path.join(home,'unused-main')}});
 try{
  const page=await client.firstWindow(),errors=[];page.on('pageerror',e=>errors.push(e.message));
  await expect(page.locator('#service-label')).toHaveText('本地服务运行中');
  await client.evaluate(({app,shell})=>{
   const require=process.mainModule.require.bind(process.mainModule),{BridgeService}=require(require('node:path').join(app.getAppPath(),'lib/service.cjs')),original=BridgeService.prototype.request;
   const fixture={ready:false,cancelled:0,opened:[],channel:'bps',session:'123',id:'',sequence:123};globalThis.loginFixture=fixture;
   shell.openExternal=async url=>{fixture.opened.push(url);};
   BridgeService.prototype.request=async function(route,method,body,...rest){
    const prompt=()=>({channel:fixture.channel,session:fixture.session,auth_url:fixture.channel==='bps'?'https://auth.openai.com/api/accounts/deviceauth/authorize?client_id=app_fnr0pYvVwwFDocDumLG3H2Bp':'https://auth.openai.com/oauth/authorize?client_id=app_EMoamEEZ73f0CkXaXp7hrann',user_code:fixture.channel==='bps'?'FIX-TUR-123':''});
    if(route==='/api/login/start'){fixture.ready=false;fixture.channel=body.channel;fixture.session=String(++fixture.sequence);return prompt();}
    if(route.startsWith('/api/login/cancel')){fixture.cancelled++;return {ok:true};}
    if(route.startsWith('/api/login/status')){
     if(fixture.ready&&!fixture.id){
      const token=[Buffer.from('{"alg":"none"}').toString('base64url'),Buffer.from(JSON.stringify({exp:4102444800,email:'login@example.test',sub:'fixture-user',client_id:'app_fnr0pYvVwwFDocDumLG3H2Bp'})).toString('base64url'),Buffer.from('synthetic-signature').toString('base64url')].join('.');
      fixture.id=(await original.call(this,'/api/accounts/import?refresh=0','POST',{access_token:token,account_id:'fixture-workspace'})).ids[0];
     }
     return {...prompt(),done:fixture.ready,active:!fixture.ready,account_id:fixture.ready?fixture.id:undefined};
    }
    return original.call(this,route,method,body,...rest);
   };
  });
  await page.locator('#login-account').click();await page.locator('#login-manual').click();await page.locator('#login-bps').click();
  await expect(page.locator('#login-code')).toHaveValue('FIX-TUR-123');await expect(page.locator('#login-code')).toHaveAttribute('readonly','');
  await expect.poll(()=>client.evaluate(()=>globalThis.loginFixture.opened.length)).toBe(1);
  await fs.mkdir(path.join(root,'build/validation'),{recursive:true});await page.locator('#modal').screenshot({path:path.join(root,'build/validation/bps-login-ui.png')});
  await page.locator('#cancel-login').click();await expect.poll(()=>client.evaluate(()=>globalThis.loginFixture.cancelled)).toBe(1);
  await page.locator('#login-account').click();await page.locator('#login-manual').click();await page.locator('#login-codex').click();await expect(page.locator('#login-device')).toBeHidden();
  await expect.poll(()=>client.evaluate(()=>globalThis.loginFixture.opened.length)).toBe(2);
  await page.keyboard.press('Escape');await expect.poll(()=>client.evaluate(()=>globalThis.loginFixture.cancelled)).toBe(2);
  await page.locator('#login-account').click();await page.locator('#login-manual').click();await page.locator('#login-bps').click();await expect.poll(()=>client.evaluate(()=>globalThis.loginFixture.opened.length)).toBe(3);
  await client.evaluate(()=>{globalThis.loginFixture.ready=true;});await expect(page.locator('#bps-saved-close')).toBeVisible();await page.locator('#bps-saved-close').click();await expect(page.locator('#modal')).not.toBeVisible();
   await expect(page.locator('.account-card')).toHaveCount(1);await expect(page.locator('.card-health')).toContainText('BPS 授权');
   await page.locator('#grant-filter').selectOption('bps');await expect(page.locator('.account-card')).toHaveCount(1);
   await expect(page.locator('#account-result-count')).toHaveText('显示 1 / 1 条授权记录');
   await page.locator('#grant-filter').selectOption('codex');await expect(page.locator('.account-card')).toHaveCount(0);
   await expect(page.locator('#account-result-count')).toHaveText('显示 0 / 1 条授权记录');
   await page.locator('#clear-account-filters').click();await expect(page.locator('.account-card')).toHaveCount(1);
   await expect(page.locator('#grant-filter')).toHaveValue('all');await expect(page.locator('#clear-account-filters')).toBeHidden();
  const snapshot=await page.evaluate(()=>window.bridge.snapshot());if(snapshot.accounts.some(a=>'access_token'in a||'refresh_token'in a||'id_token'in a))throw Error('Credentials exposed to renderer');
  await page.locator('[data-page=pelican]').click();await expect(page.locator('#pelican-accounts')).toContainText('BPS 授权');
  if(errors.length)throw Error(errors.join('\n'));
  console.log(JSON.stringify({ok:true,synthetic:true,deviceCode:true,browserAllowlist:true,cancelAndEscape:true,autoImport:true,grantLabel:true,rendererErrors:0}));
 }finally{
  const {BridgeService}=require('../lib/service.cjs'),svc=new BridgeService({home,binary:''});svc.config=JSON.parse(await fs.readFile(path.join(home,'config.json'),'utf8'));await svc.stop();
  require('./feature-license-fixture.cjs').stopFixtureProcess(client);
 }
})().catch(e=>{console.error(e);process.exitCode=1;});
