'use strict';
const { createHmac, randomUUID } = require('node:crypto');
const { setTimeout: delay } = require('node:timers/promises');
const { loginURL } = require('./login.cjs');
const { openLoginBrowser } = require('./login-browser.cjs');
const { LoginProtocol } = require('./login-protocol.cjs');
const { createCurlHTTP } = require('./login-curl.cjs');

const MAX_ACCOUNTS = 50;
function secretBytes(seed) {
  const value = String(seed || '').toUpperCase().replace(/\s/g, '').replace(/=+$/, '');
  if (!/^[A-Z2-7]{16,128}$/.test(value)) throw new Error('2FA 种子应为 Base32 密钥（字母 A–Z 和数字 2–7），不是六位验证码。');
  let bits = 0, buffer = 0; const bytes = [];
  for (const c of value) {
    buffer = (buffer << 5) | 'ABCDEFGHIJKLMNOPQRSTUVWXYZ234567'.indexOf(c); bits += 5;
    if (bits >= 8) { bits -= 8; bytes.push((buffer >>> bits) & 255); }
  }
  if (bits && (buffer & ((1 << bits) - 1))) throw new Error('2FA 种子格式不完整。');
  return Buffer.from(bytes);
}
function totp(seed, now = Date.now(), digits = 6) {
  const key = secretBytes(seed), counter = Buffer.alloc(8);
  counter.writeBigUInt64BE(BigInt(Math.floor(now / 30000)));
  const mac = createHmac('sha1', key).update(counter).digest(); key.fill(0);
  return String((mac.readUInt32BE(mac[mac.length - 1] & 15) & 0x7fffffff) % (10 ** digits)).padStart(digits, '0');
}
function parseCredentials(raw) {
  if (typeof raw !== 'string' || Buffer.byteLength(raw, 'utf8') > 65536) throw new Error('输入过大，单次最多 50 个账号。');
  const lines = raw.replace(/^\uFEFF/, '').split(/\r?\n/).map((line, index) => ({ line: line.trim(), number: index + 1 })).filter(x => x.line);
  if (!lines.length || lines.length > MAX_ACCOUNTS) throw new Error('请每行输入一个账号，单次 1–50 个。');
  const seen = new Set();
  return lines.map(({line, number}) => {
    const first = line.indexOf('----'), last = line.lastIndexOf('----');
    const email = line.slice(0, first).trim().toLowerCase();
    const password = line.slice(first + 4, last);
    const seed = line.slice(last + 4).replace(/\s/g, '').toUpperCase();
    if (first < 1 || first === last || !/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(email) || email.length > 254 || !password || password.length > 1024) throw new Error(`第 ${number} 行格式不正确，请使用 邮箱----密码----2FA种子。`);
    if (seen.has(email)) throw new Error(`第 ${number} 行邮箱重复，请保留一条。`);
    if (seed) { try { secretBytes(seed).fill(0); } catch { throw new Error(`第 ${number} 行 2FA 种子无效，请填写 Base32 密钥；未开启 2FA 可留空。`); } }
    seen.add(email); return { email, password, seed };
  });
}
const terminal = row => ['completed', 'failed', 'cancelled'].includes(row.status);
const continueName = /^(继续|Continue|Allow|允许|Authorize|授权|Confirm|确认)$/i;
async function continuePage(page) { await page.getByRole('button', { name: continueName }).click({ noWaitAfter: true, timeout: 4000 }); }

// DOM-only inspection. It returns choices and short known error categories,
// never form values, page HTML, OAuth URLs, codes or arbitrary server text.
async function inspectPage(page) {
  return page.evaluate(() => {
    const visible = el => !!(el.getClientRects().length && getComputedStyle(el).visibility !== 'hidden');
    const text = document.body?.innerText || '';
    const errors = Array.from(document.querySelectorAll('[role="alert"], [data-error], .error, [id$="-error"]')).filter(visible).map(el => el.textContent || '').join(' ');
    const failure = /incorrect|invalid|wrong|错误|不正确|无效/i.test(errors) ? 'invalid' : /too many|rate limit|过多|频繁/i.test(text) ? 'rate_limit' : '';
    const radios = Array.from(document.querySelectorAll('input[name="workspace_id"]'));
    const choices = radios.map((el, index) => ({ id: el.value, index, label: (el.closest('label')?.innerText || el.labels?.[0]?.innerText || `工作区 ${index + 1}`).trim().slice(0, 160) }));
    const buttons = location.pathname === '/workspace' ? Array.from(document.querySelectorAll('button')).filter(visible).filter(el => !/log out|sign out|退出/i.test(el.innerText)).map((el, index) => ({ id: String(index), index, label: (el.innerText || '').trim().slice(0, 160) })).filter(x => x.label) : [];
    return { failure, choices: choices.length ? choices : buttons, kind: radios.length ? 'radio' : 'button', phone: location.pathname.includes('add-phone'), challenge: !!document.querySelector('iframe[src*="captcha"], iframe[src*="challenges.cloudflare"], input[name="cf-turnstile-response"]') };
  });
}

class AutoLogin {
  constructor({ service, home, browserFactory = openLoginBrowser, httpFactory = createCurlHTTP, timeout = 15 * 60 * 1000, interval = 700, now = Date.now }) {
    this.service = service; this.home = home; this.browserFactory = browserFactory;
    this.timeout = timeout; this.interval = interval; this.now = now; this.batch = null;
    this.httpFactory = httpFactory;
  }
  busy() { return !!this.batch && !this.batch.done; }
  snapshot(id) {
    if (!this.batch || (id && id !== this.batch.id)) throw new Error('授权任务已改变，请重新打开授权面板。');
    const b = this.batch;
    return { id: b.id, channel: b.channel, mode: b.mode, done: b.done, cancelling: b.controller.signal.aborted && !b.done, rows: b.rows.map(row => ({ ...row, choices: row.choices?.map(x => ({...x})) })) };
  }
  async start(input) {
    if (this.busy()) throw new Error('已有自动授权任务，请先完成或取消。');
    if (!['codex', 'bps'].includes(input?.channel)) throw new Error('请选择 Codex 原生或 BPS 授权。');
    const mode = input.mode || 'protocol';
    if (!['protocol', 'browser'].includes(mode)) throw new Error('请选择协议授权或浏览器备用。');
    const credentials = parseCredentials(input.text);
    const prior = await this.service.request('/api/login/status');
    if (prior.active) throw new Error('已有浏览器授权，请先完成或取消。');
    // The preflight yields; check again so double IPC starts cannot race.
    if (this.busy()) throw new Error('已有自动授权任务，请先完成或取消。');
    const batch = { id: randomUUID(), channel: input.channel, mode, done: false, controller: new AbortController(), rows: credentials.map((p, index) => ({ index, email: p.email, status: 'queued', stage: 'queued', message: '等待授权', choices: [] })) };
    this.batch = batch;
    batch.task = this.run(batch, credentials);
    return this.snapshot(batch.id);
  }
  async cancel(id) {
    this.snapshot(id); const b = this.batch;
    b.controller.abort();
    await b.task;
    return this.snapshot(id);
  }
  async close() { if (this.busy()) await this.cancel(this.batch.id); }
  skip(id) {
    this.snapshot(id);
    this.batch.itemController?.abort(); return true;
  }
  async open(id) {
    this.snapshot(id);
    if (!this.batch.page || this.batch.page.isClosed()) throw new Error('当前没有可打开的授权窗口。');
    await this.batch.page.bringToFront(); return true;
  }
  choose(input) {
    this.snapshot(input?.id);
    const row = this.batch.rows.find(r => r.index === input.index && r.stage === 'workspace' && r.status === 'waiting');
    const choice = row?.choices.find(x => x.id === input.choice);
    if (!choice) throw new Error('工作区选项已改变，请重新选择。');
    this.batch.selection = { index: row.index, choice: choice.id }; return true;
  }
  async run(batch, credentials) {
    try {
      for (let index = 0; index < credentials.length; index++) {
        const person = credentials[index], row = batch.rows[index];
        if (batch.controller.signal.aborted) { row.status = 'cancelled'; row.message = '已取消'; person.password = person.seed = ''; continue; }
        batch.itemController = new AbortController();
        const signal = AbortSignal.any([batch.controller.signal, batch.itemController.signal]);
        let owned, login, http;
        const abortBrowser = () => { void owned?.close().catch(() => {}); };
        const update = (stage, message, status = 'running') => Object.assign(row, { stage, message, status });
        try {
          if (batch.mode === 'browser') {
            update('browser', '正在打开独立授权浏览器…');
            owned = await this.browserFactory(this.home, signal);
            signal.addEventListener('abort', abortBrowser, { once: true });
            batch.page = owned.page;
          } else {
            update('protocol', '正在创建独立协议会话…');
            http = await this.httpFactory({proxy:this.service.config?.proxy_url || '',signal});
          }
          signal.throwIfAborted();
          update('starting', '正在创建授权…');
          login = await this.service.request('/api/login/start', 'POST', { channel: batch.channel, expected_email: person.email, fresh: true }, 65000);
          signal.throwIfAborted();
          loginURL(login);
          if (batch.mode === 'browser') {
            await owned.page.goto(login.auth_url, { waitUntil: 'domcontentloaded', timeout: 45000 });
            await this.drive(batch, row, person, login, owned.page, update, signal);
          } else {
            const protocol = new LoginProtocol(http, signal, update);
            await protocol.run(login, person, async choices => {
              row.choices = choices; update('workspace', '请选择本次协议授权的工作区', 'waiting');
              const until = this.now() + this.timeout;
              while (this.now() < until) {
                signal.throwIfAborted();
                const selected = batch.selection?.index === row.index ? choices.find(c=>c.id===batch.selection.choice) : null;
                if (selected) { row.choices=[];batch.selection=null;row.workspace=selected.label;return selected; }
                await delay(this.interval, undefined, {signal});
              }
              throw new Error('workspace_selection_timeout');
            }, totp);
            update('saving', '正在兑换并保存授权…');
            const until = this.now() + 60000;
            while (this.now() < until) {
              signal.throwIfAborted();
              const result = await this.service.request(`/api/login/status?session=${login.session}`);
              if (result.done) {
                if (result.error || !result.account_id) throw new Error('authorization_not_saved');
                row.account_id = result.account_id; update('completed','协议授权已保存，可直接选择使用','completed'); break;
              }
              await delay(this.interval,undefined,{signal});
            }
            if(row.status!=='completed')throw new Error('authorization_save_timeout');
          }
        } catch (error) {
          row.status = signal.aborted ? 'cancelled' : 'failed';
          row.message = signal.aborted ? (batch.controller.signal.aborted ? '已取消' : '已跳过') : error.loginMessage || (['browser', 'starting'].includes(row.stage) ? '无法创建授权，请检查 Chrome / Edge、本地服务和网络；已有授权须先结束。' : '授权未完成，请重新尝试或改用手动浏览器授权。');
          // Never serialize a Playwright exception: it can include filled secrets.
        } finally {
          signal.removeEventListener('abort', abortBrowser);
          person.password = person.seed = '';
          row.choices = []; batch.selection = null; batch.page = null;
          if (login && row.status !== 'completed') await this.service.request(`/api/login/cancel?session=${login.session}`, 'POST', {}, 5000).catch(() => {});
          if (owned) { try { await owned.close(); } catch { row.cleanup_warning = '临时浏览器清理未完成，请关闭授权窗口并检查 login-profiles。'; } }
          if (http) await http.close().catch(()=>{ row.cleanup_warning='协议会话清理未完成，请退出客户端。'; });
        }
      }
    } finally {
      for (const person of credentials) person.password = person.seed = '';
      for (const row of batch.rows) if (!terminal(row)) { row.status = 'cancelled'; row.message = '已取消'; }
      batch.done = true;
    }
  }
  async drive(batch, row, person, login, page, update, signal) {
    const deadline = this.now() + this.timeout;
    const performed = new Set(); let mfaAttempts = 0, lastCounter = -1, lastPath = '', stepAt = this.now();
    const fail = message => { const error = new Error('authorization_failed'); error.loginMessage = message; throw error; };
    const once = async (key, fn) => { if (!performed.has(key)) { await fn(); performed.add(key); } };
    while (this.now() < deadline) {
      signal.throwIfAborted();
      const result = await this.service.request(`/api/login/status?session=${login.session}`, 'GET', undefined, 10000);
      if (result.done) {
        if (result.error || !result.account_id) fail(/identity|邮箱|workspace/i.test(result.error || '') ? '授权身份与输入账号不一致，未入库。请重新授权目标账号。' : '授权兑换或保存失败，请检查网络后重新授权。');
        row.account_id = result.account_id; update('completed', '授权已保存，可直接选择使用', 'completed'); return;
      }
      if (page.isClosed()) fail('授权窗口已关闭，请重新开始。');
      const url = new URL(page.url());
      if (url.pathname !== lastPath) { lastPath = url.pathname; stepAt = this.now(); }
      if (url.origin !== 'https://auth.openai.com') {
        update('waiting', url.origin === 'http://localhost:1455' || url.origin === 'http://127.0.0.1:1455' ? '正在保存授权…' : '请在浏览器完成当前登录步骤，完成后自动继续', 'waiting');
      } else {
        const info = await inspectPage(page), p = url.pathname;
        if (info.failure === 'rate_limit') fail('登录请求过于频繁；本账号已停止，请稍后重新授权。');
        if (p === '/log-in' && await page.locator('input[type=email]').count()) {
          update('email', '正在填写邮箱…');
          if (performed.has('email') && info.failure) fail('邮箱未被接受，请核对后重新授权。');
          await once('email', async () => { await page.locator('input[type=email]').fill(person.email); await continuePage(page); });
        } else if (p === '/log-in/password') {
          update('password', '正在验证密码…');
          if (performed.has('password') && info.failure) fail('密码验证未通过，请核对密码后重新授权。');
          await once('password', async () => { await page.locator('input[type=password]').fill(person.password); await continuePage(page); });
        } else if (p.startsWith('/mfa-challenge/') && await page.locator('input[name=code]').count()) {
          update('mfa', person.seed ? '正在验证本地生成的 2FA 验证码…' : '请在浏览器填写验证信息，完成后自动继续', person.seed ? 'running' : 'waiting');
          if (person.seed) {
            const counter = Math.floor(this.now() / 30000);
            if (mfaAttempts >= 2 && info.failure) fail('2FA 验证未通过，请检查密钥与系统时间后重新授权。');
            if (!mfaAttempts || (info.failure && counter !== lastCounter && mfaAttempts < 2)) {
              if (this.now() % 30000 > 25000) { update('mfa', '等待下一轮 2FA 验证码…'); }
              else { await page.locator('input[name=code]').fill(totp(person.seed, this.now())); await continuePage(page); lastCounter = counter; mfaAttempts++; }
            }
          }
        } else if (p === '/choose-an-account') {
          update('identity', '正在核对登录账号…');
          const match = page.getByRole('button').filter({ hasText: person.email });
          if (await match.count() === 1) await once('identity', () => match.click({ noWaitAfter: true }));
          else update('identity', '请选择输入邮箱对应的账号；最终身份不符将拒绝入库', 'waiting');
        } else if (p === '/basispoints/deviceauth/callback' && await page.locator('input[name^=character_]').count()) {
          update('device', '正在提交 BPS 设备码…');
          await once('device', async () => {
            const fields = page.locator('input[name^=character_]'), code = login.user_code.replace(/[^A-Za-z0-9]/g, '');
            if (await fields.count() !== code.length) fail('BPS 设备码页面发生变化，请使用手动浏览器授权。');
            for (let i = 0; i < code.length; i++) await fields.nth(i).fill(code[i]);
            await continuePage(page);
          });
        } else if (p === '/workspace' || p === '/sign-in-with-chatgpt/codex/consent') {
          const key = `${p}:${JSON.stringify(info.choices)}`;
          if (!performed.has(key) && info.choices.length) {
            const picked = batch.selection?.index === row.index ? info.choices.find(c => c.id === batch.selection.choice) : null;
            const choice = picked || (info.choices.length === 1 ? info.choices[0] : null);
            if (!choice) { row.choices = info.choices.map(({id, label}) => ({id, label})); update('workspace', '此账号有多个工作区，请选择本次授权的工作区', 'waiting'); }
            else {
              update('consent', '正在确认工作区与授权…'); row.workspace = choice.label;
              await once(key, async () => {
                if (info.kind === 'radio') { await page.locator('input[name=workspace_id]').nth(choice.index).check({ force: true }); await continuePage(page); }
                else await page.getByRole('button').filter({ hasText: choice.label }).click({ noWaitAfter: true });
              });
              row.choices = []; batch.selection = null;
            }
          }
        } else if (info.phone) update('phone', '此账号要求补充手机号，请在授权浏览器完成；可取消本次任务', 'waiting');
        else update('waiting', info.challenge ? '请在授权浏览器完成额外验证，完成后自动继续' : '等待授权页面；可打开浏览器查看并完成当前步骤', 'waiting');
        if (this.now() - stepAt > 45000 && row.status === 'running') update(row.stage, '当前步骤尚未完成，请打开授权浏览器查看；不会重复提交密码', 'waiting');
      }
      await delay(this.interval, undefined, { signal });
    }
    fail('授权等待超时（15 分钟），请重新开始。');
  }
}
module.exports = { AutoLogin, parseCredentials, totp, inspectPage };
