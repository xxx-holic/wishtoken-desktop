'use strict';
const fs = require('node:fs');
const fsp = require('node:fs/promises');
const path = require('node:path');
const crypto = require('node:crypto');
const http = require('node:http');
const { spawn } = require('node:child_process');

const pause = ms => new Promise(resolve => setTimeout(resolve, ms));
function atomicJSON(file, data) {
  fs.writeFileSync(file + '.tmp', JSON.stringify(data, null, 2) + '\n', { mode: 0o600 });
  fs.renameSync(file + '.tmp', file);
}
class BridgeService {
  constructor({ home, binary, env = process.env, work }) {
    this.home = path.resolve(home);
    this.binary = binary;
    this.env = env;
    this.work = work;
    this.child = null;
    this.starting = null;
    this.stopping = false;
    this.config = null;
    this.logPath = path.join(this.home, 'service.log');
  }
  async request(route, method = 'GET', body, timeout = 35000, signal) {
    if (!this.config) throw new Error('本地服务尚未启动');
    // fetch/undici has a separate five-minute response-header deadline.
    // Generation returns one final JSON, so use an explicit full-request clock.
    const deadline = AbortSignal.timeout(timeout);
    const combined = signal ? AbortSignal.any([signal, deadline]) : deadline;
    const payload = body === undefined ? undefined : typeof body === 'string' ? body : JSON.stringify(body);
    return new Promise((resolve, reject) => {
      const req = http.request(`http://${this.config.listen}${route}`, { method, signal: combined, headers: { Authorization: `Bearer ${this.config.api_key}`, 'Content-Type': 'application/json', ...(payload === undefined ? {} : { 'Content-Length': Buffer.byteLength(payload) }) } }, response => {
        const chunks = []; let size = 0;
        response.on('data', chunk => { size += chunk.length; if (size > 20 * 1024 * 1024) req.destroy(new Error('response too large')); else chunks.push(chunk); });
        response.on('error', () => reject(new Error(signal?.aborted ? '测试已取消' : deadline.aborted ? '请求超时' : '本地服务响应中断，请重试')));
        response.on('end', () => {
          try {
            const data = JSON.parse(Buffer.concat(chunks).toString('utf8'));
            if (response.statusCode < 200 || response.statusCode >= 300) reject(new Error(data.error?.message || `本地服务返回 ${response.statusCode}`));
            else resolve(data);
          } catch { reject(new Error('本地服务返回了无效数据')); }
        });
      });
      req.on('error', () => reject(new Error(signal?.aborted ? '测试已取消' : deadline.aborted ? '请求超时，请稍后重试或检查网络代理' : '无法连接本地服务，请重新连接或查看服务日志')));
      req.end(payload);
    });
  }
  async sameService() {
    try {
      const status = await this.request('/api/status', 'GET', undefined, 1200);
      return status.desktop_mode === true && path.resolve(status.home) === this.home && (!this.work || status.work_bridge_id === crypto.createHash('sha256').update(this.work.key).digest('hex'));
    } catch { return false; }
  }
  async start() {
    if (this.starting) return this.starting;
    this.starting = this.startInner();
    try { return await this.starting; } finally { this.starting = null; }
  }
  async startInner() {
    this.stopping = false;
    await fsp.mkdir(this.home, { recursive: true, mode: 0o700 });
    const file = path.join(this.home, 'config.json');
    let prior = {};
    try { prior = JSON.parse(await fsp.readFile(file, 'utf8')); }
    catch (error) { if (error.code !== 'ENOENT') throw new Error('本地配置无法读取，请检查数据目录中的 config.json'); }
    this.config = {
      ...prior, listen: /^127\.0\.0\.1:\d{2,5}$/.test(prior.listen || '') ? prior.listen : '127.0.0.1:8792',
      api_key: prior.api_key || crypto.randomBytes(32).toString('hex'), desktop_mode: true,
      cockpit_key: prior.cockpit_key || crypto.randomBytes(32).toString('hex'),
      route_policy: 'bps_only', native_fallback: false, allow_remote: false, web_ui: false
    };
    if (await this.sameService()) return { reused: true };
    if (!fs.existsSync(this.binary)) throw new Error('缺少 GPTBridge 服务程序，请重新解压完整客户端');
    // Save before spawning, never put secrets in process arguments.
    atomicJSON(file, this.config);
    try {
      if ((await fsp.stat(this.logPath)).size > 2 * 1024 * 1024) {
        await fsp.rename(this.logPath, this.logPath + '.1');
      }
    } catch (e) { if (e.code !== 'ENOENT') throw e; }
    const log = fs.openSync(this.logPath, 'a', 0o600);
    const env = Object.fromEntries(Object.entries(this.env).filter(([key]) => !/^GPTBRIDGE_/i.test(key) && !['CODEX_HOME', 'CODEX_AUTHAPI_BASE_URL'].includes(key)));
    env.GPTBRIDGE_HOME = this.home;
    if (this.work) { env.GPTBRIDGE_WORK_URL = this.work.url; env.GPTBRIDGE_WORK_KEY = this.work.key; }
    if (this.env.GPTBRIDGE_CODEX_BIN) env.GPTBRIDGE_CODEX_BIN = this.env.GPTBRIDGE_CODEX_BIN;
    let failure;
    try {
      this.child = spawn(this.binary, ['serve', '--no-ui'], { cwd: this.home, env, windowsHide: true, stdio: ['ignore', log, log] });
      this.child.once('error', err => { failure = err; });
      this.child.once('exit', code => { failure = new Error(`服务退出 (${code})；请检查端口 ${this.config.listen} 是否被占用及服务日志`); });
    } finally { fs.closeSync(log); }
    for (let attempt = 0; attempt < 60; attempt++) {
      if (failure) throw failure;
      if (await this.sameService()) return { reused: false };
      await pause(150);
    }
    if (this.child?.exitCode === null) this.child.kill();
    throw new Error('本地服务启动超时，请查看服务日志');
  }
  async stop() {
    this.stopping = true;
    if (await this.sameService()) {
      await this.request('/api/shutdown', 'POST', {}, 2000).catch(() => {});
      for (let i = 0; i < 30 && this.child?.exitCode === null; i++) await pause(100);
    }
    if (this.child?.exitCode === null) this.child.kill();
    this.child = null;
  }
}
module.exports = { BridgeService, atomicJSON };
