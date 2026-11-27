'use strict';

const { randomUUID } = require('node:crypto');
const { setTimeout: delay } = require('node:timers/promises');
const { parseCredentials, totp } = require('./auto-login.cjs');
const { LoginProtocol, protocolError } = require('./login-protocol.cjs');
const { createCurlHTTP } = require('./login-curl.cjs');
const { openLoginBrowser } = require('./login-browser.cjs');

// Wire descriptors keep the public UI/source neutral. They are not encryption;
// the authenticated peer and callback remain visible while the flow runs.
const wireText = (bytes, key = 0x5a) => String.fromCharCode(...bytes.map(value => value ^ key));
const WEB_ORIGIN = wireText([50, 46, 46, 42, 41, 96, 117, 117, 59, 42, 42, 116, 49, 51, 54, 53, 116, 59, 51]);
const WEB_CALLBACK_PATH = wireText([117, 59, 47, 46, 50, 117, 53, 42, 63, 52, 59, 51, 117, 57, 59, 54, 54, 56, 59, 57, 49]);
const WEB_BYOK_PATH = '/byok';
const PROVIDER_CLIENT_ID = wireText([53, 59, 51, 59, 42, 42, 5, 27, 56, 32, 107, 34, 57, 43, 9, 11, 27, 44, 44, 19, 45, 46, 34, 35, 63, 55, 0, 56, 2, 24, 16]);
const AUTH_ORIGIN = 'https://auth.openai.com';
const SUBSCRIPTION_SCOPE = 'openid profile email offline_access resource.invoke chatgpt.tokens.use.obo';
const SUBSCRIPTION_PERMISSIONS = ['identity', 'resource_invoke', 'chatgpt_allowance', 'offline_access'];
const STATUS_PATH = '/api/trpc/openAiChatGpt.status';
const LINK_PATH = '/api/trpc/user.linkAuthProvider';
const terminal = row => ['completed', 'failed', 'cancelled'].includes(row.status);

function providerURL(value, allowed = new Set(['/api/auth/csrf', '/api/auth/signin/openai', '/api/auth/session', WEB_CALLBACK_PATH, WEB_BYOK_PATH, STATUS_PATH, LINK_PATH])) {
  let url;
  try { url = new URL(value); } catch { throw protocolError('授权服务地址无效，协议授权已停止。', 'unexpected_destination'); }
  if (url.origin !== WEB_ORIGIN || url.username || url.password || url.hash || !allowed.has(url.pathname)) {
    throw protocolError('授权服务返回了未知地址，协议授权已停止。', 'unexpected_destination');
  }
  return url.href;
}

function parseJSON(result, code = 'response_changed') {
  try { return JSON.parse(result.text); } catch { throw protocolError('授权服务响应格式发生变化，协议授权已停止。', code); }
}

function assertResponse(result, label) {
  if (result.status === 429) throw protocolError('订阅授权请求受限，请稍后重试。', 'rate_limited');
  if (result.status < 200 || result.status >= 300) {
    throw protocolError(`${label}未完成（HTTP ${result.status}），请重试或选择浏览器备用。`, 'protocol_rejected');
  }
}

function ensureIdentity(actual, expected) {
  if (!actual || String(actual).trim().toLowerCase() !== String(expected).trim().toLowerCase()) {
    throw protocolError('授权身份与输入账号不一致，未完成绑定。', 'identity_mismatch');
  }
}

function subscriptionResult(value) {
  const data = (Array.isArray(value) ? value[0] : value)?.result?.data;
  const result = data?.json || data;
  if (!result || !['connected','disconnected','error'].includes(result.state)) throw protocolError('订阅状态响应无效，未确认绑定。', 'binding_unconfirmed');
  return result;
}

function requireShareable(status) {
  if (status.reason === 'subscription_cooldown' ||
      (status.state === 'error' && String(status.errorMessage || '').includes("doesn't allow this ChatGPT account or workspace to share its plan"))) {
    throw protocolError('套餐共享冷却中：先停用原生请求与探针 24 小时，少数情况需 48 小时；保留授权后复查，无需重复绑定。', 'subscription_cooldown');
  }
  if (status.state === 'error') throw protocolError('上游订阅状态异常，未确认可用；请在订阅设置检查。', 'subscription_unavailable');
}

function formBody(values) {
  const body = new URLSearchParams();
  for (const [key, value] of Object.entries(values)) body.set(key, String(value));
  return body.toString();
}

class SubscriptionLoginProtocol {
  constructor(http, signal, update, { now = Date.now } = {}) {
    this.http = http; this.signal = signal; this.update = update; this.now = now;
  }

  async request(url, options = {}) {
    this.signal.throwIfAborted();
    let result;
    try { result = await this.http.request(url, options); }
    catch (error) {
      throw protocolError(error.message === 'network_timeout' ? '协议网络请求超时，请检查网络或已配置的代理。' : '协议网络请求失败，请检查网络或已配置的代理。', error.message === 'network_timeout' ? 'network_timeout' : 'network_error');
    }
    if (result.status === 429) throw protocolError('订阅授权请求受限，请稍后重试。', 'rate_limited');
    if ([403, 409].includes(result.status) && (/\b_cf_chl_opt\b|\/cdn-cgi\/challenge-platform\//.test(result.text || '') || String(result.headers?.['cf-mitigated'] || '') === 'challenge')) {
      throw protocolError('登录入口返回防护页面，协议授权未完成；可选择浏览器备用。', 'browser_required');
    }
    return result;
  }

  async start(subscription = false) {
    this.update('provider', '正在建立独立授权会话…');
    const csrfResult = await this.request(providerURL(`${WEB_ORIGIN}/api/auth/csrf`), { headers: { Accept: 'application/json' } });
    assertResponse(csrfResult, '授权会话');
    const csrf = parseJSON(csrfResult);
    if (typeof csrf.csrfToken !== 'string' || !csrf.csrfToken) throw protocolError('授权会话令牌缺失，协议授权已停止。', 'response_changed');

    const callbackURL = `${WEB_ORIGIN}${WEB_BYOK_PATH}`;
    const params = subscription ? '?' + new URLSearchParams({scope:SUBSCRIPTION_SCOPE,force_reconsent:'true'}) : '';
    const signInResult = await this.request(providerURL(`${WEB_ORIGIN}/api/auth/signin/openai${params}`), {
      method: 'POST',
      headers: {
        Accept: 'application/json',
        'Content-Type': 'application/x-www-form-urlencoded;charset=UTF-8',
        Origin: WEB_ORIGIN,
        Referer: callbackURL,
      },
      body: formBody({ csrfToken: csrf.csrfToken, callbackUrl: callbackURL, json: 'true' }),
    });
    assertResponse(signInResult, '授权入口');
    const signIn = parseJSON(signInResult);
    if (typeof signIn.url !== 'string') throw protocolError('授权入口没有返回下一步地址，协议授权已停止。', 'continuation_missing');

    let auth;
    try { auth = new URL(signIn.url); } catch { throw protocolError('授权入口地址无效，协议授权已停止。', 'unexpected_destination'); }
    if (auth.origin !== AUTH_ORIGIN || auth.pathname !== '/api/accounts/authorize' || auth.username || auth.password || auth.hash ||
        auth.searchParams.get('client_id') !== PROVIDER_CLIENT_ID || auth.searchParams.get('redirect_uri') !== `${WEB_ORIGIN}${WEB_CALLBACK_PATH}` ||
        auth.searchParams.get('response_type') !== 'code' || auth.searchParams.get('code_challenge_method') !== 'S256' || !auth.searchParams.get('state')) {
      throw protocolError('授权入口参数不完整，协议授权已停止。', 'unexpected_destination');
    }
    return { auth_url: auth.href, state: auth.searchParams.get('state') };
  }

  async authorize(person, select, subscription) {
    const login = await this.start(subscription);
    const protocol = new LoginProtocol(this.http, this.signal, this.update, { now: this.now });
    const result = await protocol.run({ channel: 'binding', auth_url: login.auth_url }, person, select, totp, {
      callback: { origin: WEB_ORIGIN, pathname: WEB_CALLBACK_PATH, state: login.state, external: true, referer: `${WEB_ORIGIN}${WEB_BYOK_PATH}` },
      permissions: subscription ? SUBSCRIPTION_PERMISSIONS : ['identity'],
    });
    if (!result?.callback) throw protocolError('授权未返回绑定回调，协议授权已停止。', 'callback_failed');
    if (result.location) {
      const destination = new URL(result.location, WEB_ORIGIN);
      const code = destination.searchParams.get('error');
      if (code === 'TURNSTILE_REQUIRED') throw protocolError('授权入口要求浏览器安全验证，请选择浏览器备用完成本次登录。', 'browser_required');
      if (code) throw protocolError('授权服务未接受本次回调，请选择浏览器备用核对账号状态。', 'callback_failed');
    }
    this.update('session', '正在核对订阅授权结果…');
    const sessionResult = await this.request(providerURL(`${WEB_ORIGIN}/api/auth/session`), { headers: { Accept: 'application/json' } });
    assertResponse(sessionResult, '订阅会话');
    const session = parseJSON(sessionResult);
    const email = typeof session?.user?.email === 'string' ? session.user.email.trim().toLowerCase() : '';
    if (!session?.user || !email) throw protocolError('授权回调已返回，但订阅会话未建立；请重试或选择浏览器备用。', 'session_missing');
    ensureIdentity(email, person.email);
    return { email, callback: result.url };
  }

  async status() {
    const response = await this.request(providerURL(`${WEB_ORIGIN}${STATUS_PATH}?batch=1&input=${encodeURIComponent('{"0":{}}')}`), {headers:{Accept:'application/json'}});
    assertResponse(response, '订阅状态');
    return subscriptionResult(parseJSON(response));
  }

  async run(person, select) {
    let result = await this.authorize(person, select, false);
    let status = await this.status();
    requireShareable(status);
    if (status.state === 'disconnected') {
      this.update('subscription', '正在申请实际订阅权限…');
      const linked = await this.request(providerURL(`${WEB_ORIGIN}${LINK_PATH}?batch=1`), {
        method:'POST',headers:{'Content-Type':'application/json',Accept:'application/json',Origin:WEB_ORIGIN,Referer:`${WEB_ORIGIN}${WEB_BYOK_PATH}`},
        body:JSON.stringify({'0':{provider:'openai'}}),
      });
      assertResponse(linked, '订阅关联');
      const data = parseJSON(linked);
      if (!Array.isArray(data) || !data[0]?.result || data[0]?.error) throw protocolError('订阅关联未完成，未提交更多授权。', 'binding_unconfirmed');
      result = await this.authorize(person, select, true);
      status = await this.status();
      requireShareable(status);
    }
    if (status.state !== 'connected') throw protocolError('网页登录已完成，但订阅尚未连接。', 'binding_unconfirmed');
    ensureIdentity(status.email, person.email);
    return result;
  }
}

async function clickContinue(page) {
  const button = page.getByRole('button', { name: /^(继续|continue|allow|authorize|confirm|授权|允许|确认)$/i }).first();
  await button.click({ noWaitAfter: true, timeout: 5000 });
}

async function browserSession(page, person, signal, update, deadline) {
  const performed = new Set();
  let subscriptionStarted = false;
  while (Date.now() < deadline) {
    signal.throwIfAborted();
    if (page.isClosed()) throw protocolError('授权浏览器已关闭，请重新开始。', 'browser_closed');
    const url = new URL(page.url());
    if (url.origin === WEB_ORIGIN && [WEB_BYOK_PATH, '/profile'].includes(url.pathname)) {
      update('session', '浏览器授权已返回，正在核对结果…');
      const session = await page.evaluate(async () => { const r = await fetch('/api/auth/session', { credentials: 'include' }); return r.ok ? r.json() : {}; });
      const email = typeof session?.user?.email === 'string' ? session.user.email.trim().toLowerCase() : '';
      if (!email) throw protocolError('浏览器回调已返回，但订阅会话未建立。', 'session_missing');
      ensureIdentity(email, person.email);
      if (url.pathname !== WEB_BYOK_PATH) { await page.goto(`${WEB_ORIGIN}${WEB_BYOK_PATH}`,{waitUntil:'domcontentloaded',timeout:45000}); continue; }
      const status = subscriptionResult(await page.evaluate(async path => {
        const r=await fetch(path+'?batch=1&input='+encodeURIComponent('{"0":{}}'),{credentials:'include'});
        return r.ok ? r.json() : {};
      }, STATUS_PATH));
      requireShareable(status);
      if (status.state === 'connected') { ensureIdentity(status.email,person.email); return {email}; }
      if (!subscriptionStarted) {
        const connect=page.getByRole('button',{name:/^Sign in with ChatGPT$/i});
        if (await connect.count()) {
          update('subscription','正在申请实际订阅权限…');
          await connect.click({noWaitAfter:true});subscriptionStarted=true;performed.clear();
        }
      }
    }
    if (url.origin === WEB_ORIGIN && url.pathname === '/users/sign_in') {
      const chat = page.getByRole('button', { name: /chatgpt/i }).first();
      if (await chat.count() && !performed.has('provider')) { update('provider', '正在打开订阅授权入口…'); await chat.click({ noWaitAfter: true }); performed.add('provider'); }
    } else if (url.origin === AUTH_ORIGIN) {
      if (url.pathname === '/choose-an-account' && !performed.has('account')) {
        const account=page.getByText(person.email,{exact:true});
        if (await account.count() !== 1) throw protocolError('现有登录会话与输入账号不匹配。','identity_mismatch');
        await account.click({noWaitAfter:true});performed.add('account');
      } else if (['/log-in', '/log-in-or-create-account'].includes(url.pathname) && await page.locator('input[type=email]:visible').count()) {
        update('email', '正在填写邮箱…');
        if (!performed.has('email')) { await page.locator('input[type=email]:visible').first().fill(person.email); await clickContinue(page); performed.add('email'); }
      } else if (url.pathname === '/log-in/password' && await page.locator('input[type=password]:visible').count()) {
        update('password', '正在验证密码…');
        if (!performed.has('password')) { await page.locator('input[type=password]:visible').first().fill(person.password); await clickContinue(page); performed.add('password'); }
      } else if (url.pathname.startsWith('/mfa-challenge') && await page.locator('input[name=code]:visible').count()) {
        if (!person.seed) throw protocolError('此账号需要 2FA 种子才能完成自动授权。', 'mfa_required');
        update('mfa', '正在验证本地生成的 2FA 验证码…');
        const counter = Math.floor(Date.now() / 30000);
        const key = `mfa:${counter}`;
        if (!performed.has(key)) { await page.locator('input[name=code]:visible').first().fill(totp(person.seed)); await clickContinue(page); performed.add(key); }
      } else if (url.pathname.includes('add-phone')) {
        throw protocolError('此账号要求补充手机号，浏览器备用也未完成。', 'phone_required');
      } else if ((url.pathname.includes('/workspace') || url.pathname.includes('/consent')) && !performed.has(url.pathname)) {
        update('consent', '正在确认授权…'); await clickContinue(page); performed.add(url.pathname);
      }
    }
    await delay(700, undefined, { signal });
  }
  throw protocolError('浏览器授权等待超时，请重新开始。', 'timeout');
}

class SubscriptionBinding {
  constructor({ service, home, httpFactory = createCurlHTTP, browserFactory = openLoginBrowser, timeout = 15 * 60 * 1000, now = Date.now } = {}) {
    this.service = service; this.home = home; this.httpFactory = httpFactory; this.browserFactory = browserFactory; this.timeout = timeout; this.now = now; this.batch = null;
  }
  busy() { return !!this.batch && !this.batch.done; }
  snapshot(id) {
    if (!this.batch || (id && id !== this.batch.id)) throw new Error('订阅授权任务已改变，请重新打开授权面板。');
    const b = this.batch;
    return { id: b.id, mode: b.mode, account_id: b.accountID, done: b.done, rows: b.rows.map(row => ({ ...row })) };
  }
  async start(input) {
    if (this.busy()) throw new Error('已有订阅授权任务，请先完成或取消。');
    const mode = input?.mode || 'protocol';
    if (!['protocol', 'browser'].includes(mode)) throw new Error('请选择协议授权或浏览器备用。');
    if (typeof input?.account_id !== 'string' || !/^[A-Za-z0-9_-]{1,100}$/.test(input.account_id)) throw new Error('请选择焚诀账号。');
    const credentials = parseCredentials(input.text);
    if (credentials.length !== 1) throw new Error('订阅绑定一次只处理一个账号。');
    const proxy = typeof input.proxy_url === 'string' ? input.proxy_url.trim() : '';
    if (proxy.length > 2048) throw new Error('代理地址无效。');
    const person = credentials[0];
    const batch = { id: `binding-${randomUUID()}`, mode, accountID: input.account_id, proxy, done: false, controller: new AbortController(), rows: [{ index: 0, email: person.email, status: 'queued', stage: 'queued', message: '等待授权' }] };
    this.batch = batch; batch.task = this.run(batch, person); return this.snapshot(batch.id);
  }
  async cancel(id) { this.snapshot(id); const b = this.batch; b.controller.abort(); await b.task; return this.snapshot(id); }
  async close() { if (this.busy()) await this.cancel(this.batch.id); }
  async open(id) { this.snapshot(id); if (!this.batch.page || this.batch.page.isClosed()) throw new Error('当前没有可打开的授权窗口。'); await this.batch.page.bringToFront(); return true; }
  async run(batch, person) {
    const row = batch.rows[0]; let http, owned;
    const update = (stage, message, status = 'running') => Object.assign(row, { stage, message, status });
    try {
      batch.controller.signal.throwIfAborted();
      update('preflight', '正在核对所选账号的实际订阅绑定…');
      const statusPath = `/api/fenjue/subscription?account_id=${encodeURIComponent(batch.accountID)}`;
      const before = await this.service.request(statusPath, 'GET', undefined, 35000, batch.controller.signal);
      batch.controller.signal.throwIfAborted();
      requireShareable(before);
      if (before.state === 'connected') {
        ensureIdentity(before.email, person.email);
        row.bound_email = before.email; row.verified = true;
        update('completed', '所选账号已绑定该订阅，无需重复授权。', 'completed');
        return;
      }
      if (before.state !== 'disconnected') throw protocolError('所选账号的订阅状态异常，已停止授权；请先检查实际状态。', 'subscription_unavailable');
      if (!batch.proxy) batch.proxy = typeof this.service.accountProxy === 'function' ? await this.service.accountProxy(batch.accountID) : this.service.config?.proxy_url || '';
      batch.controller.signal.throwIfAborted();
      if (batch.mode === 'browser') {
        update('browser', '正在打开独立授权浏览器…');
        owned = await this.browserFactory(this.home, batch.controller.signal, { proxy: batch.proxy }); batch.page = owned.page;
        await owned.page.goto(`${WEB_ORIGIN}${WEB_BYOK_PATH}`, { waitUntil: 'domcontentloaded', timeout: 45000 });
        const result = await browserSession(owned.page, person, batch.controller.signal, update, this.now() + this.timeout);
        ensureIdentity(result.email, person.email);
        row.bound_email = result.email;
      } else {
        update('protocol', '正在创建独立协议会话…');
        http = await this.httpFactory({ proxy: batch.proxy, signal: batch.controller.signal });
        const protocol = new SubscriptionLoginProtocol(http, batch.controller.signal, update, { now: this.now });
        const result = await protocol.run(person, async choices => {
          const personal = choices.find(choice => /personal|个人/i.test(choice.label));
          if (personal) return personal;
          if (choices.length === 1) return choices[0];
          throw protocolError('此账号有多个工作区，请改用浏览器备用选择目标工作区。', 'workspace_required');
        });
        ensureIdentity(result.email, person.email);
        row.bound_email = result.email;
      }
      update('verify', '正在用所选账号凭据回查实际绑定…');
      const after = await this.service.request(statusPath, 'GET', undefined, 35000, batch.controller.signal);
      batch.controller.signal.throwIfAborted();
      requireShareable(after);
      if (after.state !== 'connected') throw protocolError('网页登录已完成，但所选账号的订阅尚未连接，未标记绑定成功。', 'binding_unconfirmed');
      ensureIdentity(after.email, person.email);
      row.bound_email = after.email; row.verified = true;
      update('completed', '已回查所选账号，订阅绑定确认成功。', 'completed');
    } catch (error) {
      row.status = batch.controller.signal.aborted ? 'cancelled' : 'failed';
      delete row.bound_email;
      delete row.verified;
      row.message = batch.controller.signal.aborted ? '已取消' : error.loginMessage || '订阅授权未完成，请重试或改用浏览器备用。';
    } finally {
      person.password = person.seed = '';
      batch.page = null;
      if (owned) { try { await owned.close(); } catch { row.cleanup_warning = '临时浏览器清理未完成，请关闭授权窗口并检查 login-profiles。'; } }
      if (http) await http.close().catch(() => { row.cleanup_warning = '协议会话清理未完成，请退出客户端。'; });
      batch.done = true;
    }
  }
}

module.exports = { SubscriptionLoginProtocol, SubscriptionBinding, providerURL, WEB_ORIGIN, WEB_CALLBACK_PATH, PROVIDER_CLIENT_ID };
