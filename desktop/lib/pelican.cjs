'use strict';
const fs = require('node:fs');
const path = require('node:path');
const { randomUUID, randomBytes } = require('node:crypto');
const http = require('node:http');
const { atomicJSON } = require('./service.cjs');
const { requireID, cleanChannel, safeError } = require('./policy.cjs');
const { workModelAllowed, workEffortAllowed } = require('./work-protocol.cjs');
const { PELICAN_PROMPT: DEFAULT_PROMPT, CANDY_PROMPT, candyVerdict } = require('./benchmarks.cjs');
const uuid = /^[0-9a-f-]{36}$/;
const active = batch => batch && ['running', 'cancelling'].includes(batch.status);
function extractHTML(raw) {
  const fenced = raw.match(/```(?:html)?\s*([\s\S]*?)```/i);
  const text = (fenced ? fenced[1] : raw).trim();
  const start = text.search(/<!doctype\s+html|<html[\s>]/i);
  const end = text.toLowerCase().lastIndexOf('</html>');
  if (start < 0 || end < start) throw new Error('模型未返回完整 HTML；可重试');
  return text.slice(start, end + 7);
}
const CSP = "default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; img-src data: blob:; font-src data:; media-src data: blob:; connect-src 'none'; frame-src 'none'; worker-src 'none'; object-src 'none'; base-uri 'none'; form-action 'none'; sandbox allow-scripts";
const lockdown = `<script>for(const n of ['RTCPeerConnection','webkitRTCPeerConnection','WebTransport','WebSocket','Worker','SharedWorker','open','alert','confirm','prompt','print']){try{Object.defineProperty(window,n,{value:undefined,writable:false,configurable:false});}catch{}}</script>`;
class Pelican {
  constructor(home, generate) {
    this.home = path.join(home, 'pelican'); this.generate = generate; this.controllers = new Map(); this.token = randomBytes(24).toString('hex');
    fs.mkdirSync(this.home, { recursive: true, mode: 0o700 }); this.file = path.join(this.home, 'history.json');
    try { this.batches = JSON.parse(fs.readFileSync(this.file, 'utf8')); if (!Array.isArray(this.batches)) throw new Error('Invalid history'); }
    catch (e) { if (e.code !== 'ENOENT') throw new Error('测智历史无法读取，请检查 pelican/history.json'); this.batches = []; }
    for (const b of this.batches) {
      b.channel ??= 'bps';
      b.benchmark ??= 'pelican';
      if (active(b)) { b.status = 'interrupted'; for (const i of b.items) if (['queued', 'running'].includes(i.status)) i.status = 'interrupted'; }
    }
    this.save();
  }
  save() { atomicJSON(this.file, this.batches); }
  snapshot() { return this.batches.map(b => ({ ...b, items: b.items.map(i => ({ ...i, preview: b.benchmark !== 'candy' && i.status === 'completed' && this.origin ? `${this.origin}/${this.token}/${i.id}` : null })) })); }
  async listen() {
    this.server = http.createServer((req, res) => {
      const id = req.url?.split('/')[2];
      if (req.method !== 'GET' || req.url !== `/${this.token}/${id}` || !uuid.test(id || '') || req.headers.host !== `127.0.0.1:${this.server.address().port}`) { res.writeHead(404).end(); return; }
      try { const html = this.artifact(id).html; if (!html) throw new Error('No HTML'); res.writeHead(200, { 'Content-Type': 'text/html; charset=utf-8', 'Content-Security-Policy': CSP, 'Cache-Control': 'no-store', 'Referrer-Policy': 'no-referrer', 'X-Content-Type-Options': 'nosniff' }); res.end(lockdown + html); }
      catch { res.writeHead(404).end(); }
    });
    await new Promise((resolve, reject) => { this.server.once('error', reject); this.server.listen(0, '127.0.0.1', resolve); });
    this.origin = `http://127.0.0.1:${this.server.address().port}`;
  }
  allowedPreview(url) { try { const u = new URL(url); return u.origin === this.origin && u.pathname.startsWith(`/${this.token}/`) && uuid.test(u.pathname.split('/')[2]); } catch { return false; } }
  artifact(id) {
    if (!uuid.test(id || '') || !this.batches.some(b => b.items.some(i => i.id === id && (i.status === 'completed' || i.has_artifact)))) throw new Error('测试结果不存在');
    return JSON.parse(fs.readFileSync(path.join(this.home, `${id}.json`), 'utf8'));
  }
  start(input, accounts) {
    if (this.batches.some(active)) throw new Error('已有测试在运行，请等待完成或取消');
    const ids = [...new Set(input?.account_ids || [])];
    if (!ids.length || ids.length > 20) throw new Error('请选择 1–20 个测试账号');
    const chosen = ids.map(id => { requireID(id); const a = accounts.find(a => a.id === id); if (!a || a.disabled || (a.expired && !a.has_refresh_token)) throw new Error('选中的账号已不可用'); return a; });
    if (!/^[a-zA-Z0-9.-]{1,80}$/.test(input.model || '') || !['low', 'medium', 'high', 'xhigh', 'max'].includes(input.effort)) throw new Error('模型或档位无效');
    const benchmark = input.benchmark || 'pelican';
    if (!['pelican','candy'].includes(benchmark)) throw new Error('测试题类型无效');
    const prompt = (input.prompt || (benchmark === 'candy' ? CANDY_PROMPT : DEFAULT_PROMPT)).trim();
    if (!prompt || Buffer.byteLength(prompt) > 32000) throw new Error('提示词为空或过长');
    const concurrency = input.concurrency ?? 2;
    const channel = cleanChannel(input.channel);
    if (chosen.some(a => (a.provider === 'kilo') !== (channel === 'kilo'))) throw new Error('Kilo 测试请选择独立 Kilo 账号，其他通道请选择 ChatGPT 账号');
    if (channel === 'work' && (!workModelAllowed(input.model) || !workEffortAllowed(input.effort) || concurrency !== 1)) throw new Error('Work 测试请选择受支持的 Work 模型与推理档位，同时测试数须为 1');
    if (channel !== 'work' && input.effort === 'max') throw new Error('仅 Work 通道支持 max 推理档位');
    if (!Number.isInteger(concurrency) || concurrency < 1 || concurrency > 5) throw new Error('同时测试数应为 1–5');
    const batch = { id: randomUUID(), created_at: new Date().toISOString(), benchmark, channel, model: input.model, effort: input.effort, prompt, concurrency, status: 'running', items: chosen.map(a => ({ id: randomUUID(), account_id: a.id, name: a.name || a.email || a.id, status: 'queued' })) };
    this.batches.unshift(batch); this.save();
    this.running = this.run(batch);
    return batch.id;
  }
  async run(batch) {
    await Promise.all(Array.from({ length: batch.concurrency }, async () => {
      while (batch.status === 'running') {
        const item = batch.items.find(i => i.status === 'queued'); if (!item) break;
        const controller = new AbortController(); this.controllers.set(item.id, controller);
        item.status = 'running'; item.started_at = new Date().toISOString(); this.save();
        try {
          const result = await this.generate({ account_id: item.account_id, channel: batch.channel, model: batch.model, effort: batch.effort, prompt: batch.prompt, benchmark:batch.benchmark }, controller.signal);
          if (controller.signal.aborted) throw new Error('测试已取消');
          if (typeof result.text !== 'string' || Buffer.byteLength(result.text) > 8 * 1024 * 1024) throw new Error('生成结果过大或为空');
          const artifact = { ...result, channel:batch.channel, benchmark:batch.benchmark };
          // Preserve the upstream answer even when it is wrong or has invalid HTML.
          atomicJSON(path.join(this.home, `${item.id}.json`), artifact); item.has_artifact=true;
          if (batch.benchmark === 'candy') {
            Object.assign(item,candyVerdict(result.text,batch.prompt)); item.answer_text=result.text;
          } else { artifact.html=extractHTML(result.text); atomicJSON(path.join(this.home, `${item.id}.json`),artifact); }
          item.status = 'completed'; item.usage = result.usage; item.duration_ms = result.duration_ms; item.response_model = result.response_model || null; item.route = result.route || batch.channel; item.transport = result.transport || null;
        } catch (error) { item.status = controller.signal.aborted ? 'cancelled' : 'failed'; item.error = safeError(error); }
        finally { item.finished_at = new Date().toISOString(); this.controllers.delete(item.id); this.save(); }
      }
    }));
    batch.status = batch.status === 'cancelling' ? 'cancelled' : 'completed'; batch.finished_at = new Date().toISOString(); this.save();
  }
  cancel() {
    const batch = this.batches.find(active); if (!batch) return;
    batch.status = 'cancelling';
    for (const i of batch.items) if (i.status === 'queued') i.status = 'cancelled';
    for (const c of this.controllers.values()) c.abort();
    this.save();
  }
  remove(id) {
    const batch = this.batches.find(b => b.id === id); if (!batch || active(batch)) throw new Error('请先停止测试');
    this.batches = this.batches.filter(b => b !== batch); this.save();
    for (const i of batch.items) if (uuid.test(i.id)) fs.rmSync(path.join(this.home, `${i.id}.json`), { force: true });
  }
  async close() { this.cancel(); await this.running; if (this.server) await new Promise(resolve => this.server.close(resolve)); }
}
module.exports = { Pelican, DEFAULT_PROMPT, extractHTML, CSP };
