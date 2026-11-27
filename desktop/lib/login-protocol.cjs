'use strict';
const { randomUUID } = require('node:crypto');
const { setTimeout: delay, setImmediate: yieldLoop } = require('node:timers/promises');
const { loginProblem } = require('./login-errors.cjs');
const AUTH = 'https://auth.openai.com', SENTINEL = 'https://sentinel.openai.com';
function protocolError(message, code = 'protocol_incomplete') { const error = new Error(code); error.loginMessage = message; error.loginCode = code; return error; }
function authURL(value) {
  const url = new URL(value, AUTH);
  if (url.origin !== AUTH || url.username || url.password) throw protocolError('登录服务返回了未知地址，协议授权已停止。', 'unexpected_destination');
  return url.href;
}
function nextURL(value) {
  for (const item of [value, value?.page, value?.page?.payload]) if (item?.continue_url || item?.url) return new URL(item.continue_url || item.url, AUTH).href;
  return '';
}
function factorID(value, url) {
  for (const item of [value, value?.page, value?.page?.payload]) {
    if (item?.factor_id) return String(item.factor_id);
    const factor = (item?.factors || item?.mfa_challenge_factors || []).find(x => ['totp','authenticator'].includes(x.type || x.factor_type));
    if (factor) return String(factor.id || factor.factor_id || '');
  }
  return new URL(url, AUTH).pathname.match(/^\/mfa-challenge\/([^/]+)$/)?.[1] || '';
}
function workspaces(body, cookies = []) {
  const candidates = [body?.['oai-client-auth-session'], body, body?.page?.payload];
  for (const cookie of cookies.filter(c => c.name === 'oai-client-auth-session')) {
    for (const segment of decodeURIComponent(cookie.value).split('.')) { try { candidates.push(JSON.parse(Buffer.from(segment,'base64url'))); } catch {} }
  }
  const source = candidates.find(x => Array.isArray(x?.workspaces));
  return [...new Map((source?.workspaces || []).filter(x => typeof x.id === 'string').map(w => [w.id,{id:w.id,label:String(w.title || w.name || (w.is_personal ? 'Personal' : '工作区')).slice(0,160)}])).values()];
}
function existingSession(html, email) {
  const matches = [...String(html || '').matchAll(/<button\b([^>]*)>([\s\S]*?)<\/button>/gi)]
    .filter(([, attrs, content]) => /\bname=["']session_id["']/i.test(attrs) &&
      content.replace(/<[^>]*>/g, ' ').split(/\s+/).some(value => value.toLowerCase() === email.toLowerCase()))
    .map(([, attrs]) => attrs.match(/\bvalue=["']([A-Za-z0-9_-]{1,255})["']/i)?.[1]).filter(Boolean);
  if (matches.length !== 1) throw protocolError('现有登录会话与输入账号不匹配，请使用浏览器备用核对。', 'identity_mismatch');
  return matches[0];
}
function fnvMix(text) {
  let h = 2166136261;
  for (const c of text) h = Math.imul(h ^ c.charCodeAt(0), 16777619) >>> 0;
  h = Math.imul(h ^ (h >>> 16), 2246822507) >>> 0;
  h = Math.imul(h ^ (h >>> 13), 3266489909) >>> 0;
  return ((h ^ (h >>> 16)) >>> 0).toString(16).padStart(8,'0');
}
class LoginProtocol {
  constructor(http, signal, update, {now=Date.now, onEvidence=()=>{}} = {}) {
    this.http=http;this.signal=signal;this.update=update;this.now=now;this.evidence=onEvidence;
    this.device=randomUUID();this.sid=randomUUID();this.context={};this.started=now();
  }
  async request(url, options={}) {
    this.signal.throwIfAborted();
    if (this.now()-this.started > 10*60*1000) throw protocolError('协议授权超时，请重新开始。','timeout');
    let result;
    try { result=await this.http.request(url,options); }
    catch(error) { throw protocolError(error.message==='network_timeout'?'协议网络请求超时，请检查网络或已配置的代理。':'协议网络请求失败，请检查网络或已配置的代理。',error.message==='network_timeout'?'network_timeout':'network_error'); }
    this.evidence({path:new URL(url).pathname,method:options.method||'GET',status:result.status});
    if(result.status===429)throw protocolError('登录请求受限，请稍后重试。','rate_limited');
    if([403,409].includes(result.status) && (/\b_cf_chl_opt\b|\/cdn-cgi\/challenge-platform\//.test(result.text||'')||String(result.headers?.['cf-mitigated']||'')==='challenge'))throw protocolError('登录入口返回防护页面，协议授权未完成；可稍后重试或明确选择浏览器备用。','browser_required');
    return result;
  }
  config() {
    const perf=this.now()-this.started;
    return ['1920x1080',new Date(this.now()).toString(),4294967296,1,this.http.userAgent,'https://sentinel.openai.com/sentinel/20260124ceb8/sdk.js',null,null,'en-US','en-US,en',Math.random(),'hardwareConcurrency−undefined','location','Object',perf,this.sid,'',require('node:os').availableParallelism(),this.now()-perf,0,0,0,0,0,0];
  }
  encode(value) { return Buffer.from(JSON.stringify(value)).toString('base64'); }
  async security(flow) {
    const initial=this.config();initial[9]=10;
    const p='gAAAAAC'+this.encode(initial);
    const r=await this.request(SENTINEL+'/backend-api/sentinel/req',{method:'POST',headers:{'Content-Type':'text/plain;charset=UTF-8',Accept:'*/*',Origin:SENTINEL,Referer:SENTINEL+'/backend-api/sentinel/frame.html'},body:JSON.stringify({p,id:this.device,flow})});
    let challenge;try{challenge=JSON.parse(r.text);}catch{}
    if(r.status!==200||!challenge?.token)throw protocolError('登录安全验证暂不可用，请稍后重试。','security_unavailable');
    let proof=p;const pow=challenge.proofofwork;
    if(pow?.required){
      if(typeof pow.seed!=='string'||!/^\d?[a-f0-9]{1,8}$/i.test(pow.difficulty||''))throw protocolError('安全验证参数发生变化，协议授权已停止。','security_changed');
      const config=this.config(),start=this.now();let solved=false;
      for(let nonce=0;nonce<500000;nonce++){
        config[3]=nonce;config[9]=this.now()-start;
        const encoded=this.encode(config);
        if(fnvMix(pow.seed+encoded).slice(0,pow.difficulty.length)<=pow.difficulty){proof='gAAAAAB'+encoded+'~S';solved=true;break;}
        if(nonce%1024===0){this.signal.throwIfAborted();await yieldLoop();}
      }
      if(!solved)throw protocolError('安全验证计算未完成，请稍后重试。','security_unavailable');
    }
    // Same protocol shape as the referenced service. Server-side acceptance is
    // checked at every step; a required unsolved challenge is never called success.
    return JSON.stringify({p:proof,t:null,c:challenge.token,id:this.device,flow});
  }
  async post(path, body, referer, flow, extraHeaders = {}) {
    const headers={'Content-Type':'application/json',Accept:'application/json',Origin:AUTH,Referer:authURL(referer),...extraHeaders};
    if(flow)headers['openai-sentinel-token']=await this.security(flow);
    const r=await this.request(authURL(path),{method:'POST',headers,body:JSON.stringify(body)});
    let data;try{data=JSON.parse(r.text);}catch{}
    const code=typeof data?.error === 'string' ? data.error : data?.error?.code || data?.code;
    if(r.status<200||r.status>=300){
      const problem = loginProblem(code);
      if (problem) throw protocolError(problem.message, problem.code);
      const messages={invalid_username_or_password:'邮箱或密码验证失败，请核对后重试。',incorrect_password:'密码验证失败，请核对后重试。',invalid_password:'密码验证失败，请核对后重试。',invalid_totp:'2FA 验证失败，请检查密钥和系统时间。',incorrect_totp:'2FA 验证失败，请检查密钥和系统时间。',invalid_code:'验证码未通过，请核对后重试。',unsupported_country_region_territory:'当前网络出口地区不受登录服务支持。'};
      throw protocolError(messages[code]||`协议步骤未完成（HTTP ${r.status}），本账号已停止；可重新尝试或选择浏览器备用。`,messages[code]?code:'protocol_rejected');
    }
    if(!data)throw protocolError('登录服务响应格式发生变化，未继续提交。','response_changed');
    if(data['oai-client-auth-session'])this.context=data['oai-client-auth-session'];
    return data;
  }
  async follow(url, callback) {
    for(let hop=0;hop<15;hop++){
      const parsed=new URL(url,AUTH);
      if(parsed.origin==='http://localhost:1455'||parsed.origin==='http://127.0.0.1:1455'){
        if(!callback||parsed.origin!==callback.origin||parsed.pathname!==callback.pathname||parsed.searchParams.get('state')!==callback.state)throw protocolError('授权回调不匹配，未兑换凭据。','identity_mismatch');
        const r=await this.request(parsed.href);if(r.status!==200)throw protocolError('本地授权兑换未完成，请检查本地服务与登录代理。','callback_failed');return {url:parsed.href,text:r.text};
      }
      // A web OAuth consumer can terminate on its own callback host instead
      // of the local Codex listener.  Accept exactly the callback origin and
      // path supplied by the caller, validate state/code, and stop there so
      // the consumer's server-side session exchange can run in its cookie jar.
      if(callback?.external && parsed.origin===callback.origin && parsed.pathname===callback.pathname){
        if(callback.state && parsed.searchParams.get('state')!==callback.state)throw protocolError('授权回调状态不匹配，未继续绑定。','identity_mismatch');
        if(!parsed.searchParams.get('code'))throw protocolError('授权回调缺少授权码，未继续绑定。','callback_failed');
        const r=await this.request(parsed.href,{headers:{Accept:'text/html,application/json',Origin:callback.origin,Referer:callback.referer||callback.origin+'/'} });
        if(r.status<200||r.status>=400)throw protocolError(`授权回调未完成（HTTP ${r.status}），请重试或选择浏览器备用。`,'callback_failed');
        return {url:parsed.href,text:r.text,status:r.status,location:r.location,callback:true};
      }
      url=authURL(parsed.href);
      const r=await this.request(url,{headers:{Accept:'text/html,application/json'}});
      if(r.location){url=new URL(r.location,url).href;continue;}
      if(r.status!==200)throw protocolError(`登录页面返回 HTTP ${r.status}，请检查网络后重试。`,'entry_rejected');
      if(r.text.trim().startsWith('{')){
        let body;try{body=JSON.parse(r.text);}catch{}
        if(body?.['oai-client-auth-session'])this.context=body['oai-client-auth-session'];
        const next=nextURL(body);
        if(next&&next!==url&&(!body.method||body.method==='GET')){
          if(/^\/(log-in|mfa-challenge|workspace|sign-in-with-chatgpt\/codex\/consent)(\/|$)/.test(new URL(next).pathname))return {url:authURL(next),text:r.text};
          url=next;continue;
        }
      }
      return {url,text:r.text};
    }
    throw protocolError('授权重定向次数过多，已停止。','redirect_limit');
  }
  async approveDevice(login, page) {
    const url = new URL(authURL(page.url));
    const code = String(login.user_code || '').replace(/-/g, '').toUpperCase();
    const authorization = url.searchParams.get('code'), state = url.searchParams.get('state');
    const client = new URL(login.auth_url).searchParams.get('client_id');
    if (login.channel !== 'bps' || client !== 'app_fnr0pYvVwwFDocDumLG3H2Bp' ||
        !/^[A-Z0-9]{9}$/.test(code) || !authorization || !state) {
      throw protocolError('BPS 设备批准参数不完整，请重新创建授权。', 'device_approval_invalid');
    }
    this.update('device', '协议批准本次 BPS 设备授权…');
    const body = new URLSearchParams({user_code: code.match(/.{3}/g).join('-'), user_code_text: code,
      decision: 'grant', device_code: code, authorization_code: authorization, state, client_id: client});
    for (let i = 0; i < code.length; i++) body.set(`character_${i + 1}`, code[i]);
    const token = await this.security('device_auth');
    const response = await this.request(AUTH + '/api/accounts/deviceauth/authorize_code', {
      method: 'POST', headers: {'Content-Type': 'application/x-www-form-urlencoded;charset=UTF-8',
        Accept: 'application/json', Origin: AUTH, Referer: url.href, 'openai-sentinel-token': token}, body: body.toString()
    });
    if (response.status < 200 || response.status >= 300) {
      throw protocolError(`BPS 设备批准未完成（HTTP ${response.status}），请重新创建授权。`, 'device_approval_rejected');
    }
    // This is approval only. AutoLogin still waits for the backend's separate
    // device poll, PKCE exchange and identity-checked store before reporting ready.
  }
  async grantConsent(page, permissions = ['identity']) {
    const context = this.context?.oauth_resource_consent?.selection?.context || this.context?.oauth_consent_availability?.context;
    const inputs = [...String(page.text || '').matchAll(/<input\b[^>]*>/gi)];
    const sessions = inputs.filter(([tag]) => /\bname=["']auth_session_id["']/i.test(tag))
      .map(([tag]) => tag.match(/\bvalue=["'](authsess_[A-Za-z0-9]+)["']/i)?.[1]);
    const session = sessions.length === 1 ? sessions[0] : '';
    const allowed = new Set(['identity', 'resource_invoke', 'chatgpt_allowance', 'offline_access']);
    if (!session || !Array.isArray(permissions) || !permissions.length || permissions.some(value => !allowed.has(value))) {
      throw protocolError('订阅同意页缺少本次会话参数，未提交授权。', 'consent_context_missing');
    }
    this.update('consent', '正在确认本次订阅授权权限…');
    // Ordinary identity consent has no selectable context. Submit exactly the
    // observed form session; only resource consent carries a permission selection.
    if (!context) {
      if (permissions.some(value => value !== 'identity')) throw protocolError('订阅权限上下文缺失，未提交授权。', 'consent_context_missing');
      return this.post('/api/accounts/consent/grant', {auth_session_id: session}, page.url, 'authorize_continue');
    }
    const availability = await this.post('/api/accounts/consent/availability', {auth_session_id: session, context}, page.url, 'authorize_continue',
      {'openai-consent-decision': 'v1', 'openai-consent-display': 'workspace_name'});
    if (availability.context !== context) throw protocolError('订阅权限上下文已改变，未提交授权。', 'consent_context_missing');
    return this.post('/api/accounts/consent/grant', {auth_session_id: session,
      permission_selection: {context, selected_permissions: permissions,
        ...(availability.availability_receipt ? {availability_receipt: availability.availability_receipt} : {})}}, page.url, 'authorize_continue');
  }
  async run(login, person, select, totp, options = {}) {
    const auth=new URL(login.auth_url);
    const redirect=login.channel==='codex'?new URL(auth.searchParams.get('redirect_uri')):options.callback?.origin?new URL(options.callback.origin):null;
    const callback=redirect?{origin:redirect.origin,pathname:options.callback?.pathname||redirect.pathname,state:auth.searchParams.get('state'),external:Boolean(options.callback?.external),referer:options.callback?.referer}:null;
    this.update('protocol','正在通过协议建立登录会话…');
    let page=await this.follow(login.auth_url,callback),payload={};
    const did=(await this.http.cookies(AUTH)).find(c=>c.name==='oai-did');if(did)this.device=did.value;
    for(let step=0;step<16;step++){
      this.signal.throwIfAborted();const p=new URL(page.url).pathname;
      if(p==='/auth/callback'&&callback)return;
      if(p==='/log-in'||p==='/log-in-or-create-account'||p==='/sign-in'||p==='/u/login/identifier'){
        this.update('email','协议校验邮箱…');payload=await this.post('/api/accounts/authorize/continue',{username:{value:person.email,kind:'email'}},page.url,'authorize_continue');
      }else if(p==='/choose-an-account'){
        this.update('account','正在确认本次授权的账号身份…');
        payload=await this.post('/api/accounts/session/select',{session_id:existingSession(page.text,person.email)},page.url,'authorize_continue');
      }else if(p==='/log-in/password'){
        this.update('password','协议验证密码…');payload=await this.post('/api/accounts/password/verify',{password:person.password},page.url,'password_verify');
      }else if(p.startsWith('/mfa-challenge')){
        this.update('mfa','协议验证本地生成的 2FA 验证码…');
        const id=factorID(payload,page.url);if(!id||!person.seed)throw protocolError('此账号需要有效的 2FA 种子才能完成协议授权。','mfa_required');
        await this.post('/api/accounts/mfa/issue_challenge',{id,type:'totp',force_fresh_challenge:false},page.url);
        // Acquire the security token first; calculate TOTP only immediately before submit.
        const token=await this.security('password_verify');
        if(this.now()%30000>27000)await delay(31000-this.now()%30000,undefined,{signal:this.signal});
        const r=await this.request(AUTH+'/api/accounts/mfa/verify',{method:'POST',headers:{'Content-Type':'application/json',Accept:'application/json',Origin:AUTH,Referer:page.url,'openai-sentinel-token':token},body:JSON.stringify({id,type:'totp',code:totp(person.seed,this.now())})});
        try{payload=JSON.parse(r.text);}catch{}
        if(r.status!==200)throw protocolError('2FA 协议验证未通过，请检查密钥、系统时间或使用浏览器备用。','mfa_rejected');
        if(payload?.['oai-client-auth-session'])this.context=payload['oai-client-auth-session'];
      }else if(p.includes('email-verification')||p.includes('email-otp'))throw protocolError('登录服务另外要求邮箱验证码；当前资料不足，请选择浏览器备用完成。','email_verification_required');
      else if(p.includes('add-phone'))throw protocolError('此账号要求补充手机号，协议授权已停止；可选择浏览器备用处理。','phone_required');
      else if(login.channel==='binding'&&p==='/sign-in-with-chatgpt/consent'){
        payload=await this.grantConsent(page, options.permissions);
      }else if(p.includes('workspace')||p.includes('consent')){
        const choices=workspaces(this.context,await this.http.cookies(AUTH));
        if(!choices.length)throw protocolError('协议响应中未找到工作区，请使用浏览器备用核对。','workspace_missing');
        const choice=choices.length===1?choices[0]:await select(choices);
        this.update('consent','协议确认所选工作区…');
        payload=await this.post('/api/accounts/workspace/select',{workspace_id:choice.id},page.url);
      }else if(p==='/basispoints/deviceauth/callback'){
        await this.approveDevice(login,page);return;
      }else throw protocolError('遇到尚未支持的登录步骤，请选择浏览器备用继续。','unsupported_step');
      const next=nextURL(payload);if(!next)throw protocolError('登录响应没有下一步地址，协议授权已停止。','continuation_missing');
      page=await this.follow(next,callback);
      if(page.callback)return page;
    }
    throw protocolError('协议登录步骤超出上限，已停止。','step_limit');
  }
}
module.exports={LoginProtocol,protocolError,authURL,nextURL,factorID,workspaces,fnvMix};
