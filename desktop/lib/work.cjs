'use strict';
const fs = require('node:fs');
const path = require('node:path');
const http = require('node:http');
const { createHash, randomBytes, timingSafeEqual } = require('node:crypto');
const { atomicJSON } = require('./service.cjs');
const { requireID, safeError } = require('./policy.cjs');
const { MODEL, MODELS, EFFORT_LABELS, modelTitle, workModelAllowed, planRequest, responseFor, responseEvents } = require('./work-protocol.cjs');
const pause = ms => new Promise(r => setTimeout(r,ms));
function endpoint(value) {
  const u = new URL(value || 'http://127.0.0.1:9222');
  if (u.protocol !== 'http:' || u.hostname !== '127.0.0.1' || !u.port || u.username || u.password || u.pathname !== '/' || u.search || u.hash) throw new Error('浏览器地址必须是 http://127.0.0.1:端口');
  return u.origin;
}
// Runs in the authorized page. Credentials never leave the page execution context.
async function pageIdentity() {
  const r = await fetch('/api/auth/session'); const s = await r.json();
  if (!r.ok || !s.accessToken || !s.user?.email || !s.account?.id) throw new Error('请先在浏览器登录 ChatGPT，并选择工作区');
  return { email:s.user.email.toLowerCase(), workspace:s.account.id };
}
async function pasteImages({ images }) {
  const editor = document.querySelector('[contenteditable="true"]');
  if (!editor) throw new Error('Work 编辑器未找到');
  editor.focus();
  for (const dataUrl of images) {
    const match = dataUrl.match(/^data:(image\/[a-z0-9.+-]+);base64,(.+)$/i);
    if (!match) throw new Error('图像格式无效');
    const binary = atob(match[2]);
    const bytes = new Uint8Array(binary.length);
    for (let i = 0; i < binary.length; i++) bytes[i] = binary.charCodeAt(i);
    const extension = match[1].split('/')[1].replace('jpeg','jpg');
    const file = new File([bytes], `image-${Date.now()}-${Math.random().toString(16).slice(2)}.${extension}`, { type: match[1] });
    const transfer = new DataTransfer();
    transfer.items.add(file);
    editor.dispatchEvent(new ClipboardEvent('paste', { clipboardData: transfer, bubbles:true, cancelable:true }));
    await new Promise(resolve => setTimeout(resolve, 800));
  }
}
function composerImages() {
  return [...document.querySelectorAll('img')].filter(image => {
    const source = image.getAttribute('src') || '';
    return source.startsWith('blob:') || source.startsWith('data:image');
  }).length;
}
async function pageBackend(route) {
  const s = await (await fetch('/api/auth/session')).json();
  if (!s.accessToken) throw new Error('ChatGPT 登录已失效');
  const r = await fetch(route,{ headers:{ authorization:'Bearer '+s.accessToken } });
  return { status:r.status, data:await r.json() };
}
function mergeContent(content, text) {
  const parts = Array.isArray(content?.parts) ? content.parts : [];
  const extras = parts.filter(part => !(typeof part === 'string' || (part && ['text','input_text','output_text'].includes(part.type))));
  return { ...(content || {}), content_type:'text', parts:[text, ...extras] };
}
function capture({ prompt, model, effort, identity }) {
  if (window.__wishWork) throw new Error('Work 页面已有请求，请等待完成');
  const original = window.fetch;
  const state = window.__wishWork = { original, traffic:[] };
  window.fetch = async function(input, options) {
    const pathname = new URL(typeof input === 'string' ? input : input.url,location.href).pathname;
    let row;
    if (pathname.startsWith('/backend-api/f/conversation')) {
      let body; try { body = JSON.parse(options?.body || 'null'); } catch {}
      if (pathname.endsWith('/prepare') && body?.partial_query?.author?.role === 'user') {
        body.partial_query.content = mergeContent(body.partial_query.content, prompt);
        options = { ...options, body:JSON.stringify(body) };
      }
      if (pathname === '/backend-api/f/conversation') {
        if (body?.messages?.length !== 1 || body.messages[0].author?.role !== 'user') throw new Error('Work 消息结构改变，已停止发送');
        // Restore the original client text after rich-editor Markdown escaping,
        // while keeping image asset pointers the web editor attached.
        body.messages[0].content = mergeContent(body.messages[0].content, prompt);
        options = { ...options, body:JSON.stringify(body) };
        if (body.model !== model || body.thinking_effort !== effort || body.conversation_origin !== 'tpp') throw new Error('Work 模型、档位或入口不匹配，未发送');
        const session = await (await original.call(window,'/api/auth/session')).json();
        const workspaceHeader = new Headers(options?.headers || {}).get('chatgpt-account-id');
        if (session.user?.email?.toLowerCase() !== identity.email || session.account?.id !== identity.workspace || (workspaceHeader && workspaceHeader !== identity.workspace)) throw new Error('Work 发送前身份或工作区改变，未发送');
      }
      row = { path:pathname, body }; state.traffic.push(row);
    }
    const result = await original.call(this,input,options);
    if (row) row.status = result.status;
    return result;
  };
}
class Work {
  constructor(home) {
    this.home = path.join(home,'work'); fs.mkdirSync(this.home,{ recursive:true, mode:0o700 });
    this.file = path.join(this.home,'bindings.json');
    try { this.bindings = JSON.parse(fs.readFileSync(this.file,'utf8')); }
    catch (e) { if (e.code !== 'ENOENT') throw new Error('Work 绑定记录无法读取'); this.bindings = {}; }
    this.connections = new Map(); this.owned = new Map(); this.busy = new Set(); this.key = randomBytes(32).toString('hex');
  }
  chromium() { return require('playwright-core').chromium; }
  snapshot() { return Object.entries(this.bindings).map(([account_id,b]) => ({ account_id, endpoint:b.endpoint || '专用登录浏览器', workspace:b.workspace, connected:true, busy:this.busy.has(this.bindingKey(b)), model:MODEL, models:b.models || [] })); }
  bindingKey(b) { return b.owned ? 'owned:'+b.account_id : b.endpoint+'/'+b.target_id; }
  async browser(url) {
    url = endpoint(url);
    let browser = this.connections.get(url);
    if (!browser?.isConnected()) { browser = await this.chromium().connectOverCDP(url,{ timeout:10000 }); this.connections.set(url,browser); }
    return browser;
  }
  async tabs(url) {
    const browser = await this.browser(url); const rows = [];
    for (const context of browser.contexts()) for (const page of context.pages()) {
      if (!page.url().startsWith('https://chatgpt.com/')) continue;
      const cdp = await context.newCDPSession(page);
      try { const { targetInfo } = await cdp.send('Target.getTargetInfo'); rows.push({ id:targetInfo.targetId, title:await page.title(), url:page.url() }); }
      finally { await cdp.detach(); }
    }
    return rows;
  }
  async ownedPage(id) {
    requireID(id);
    let context = this.owned.get(id);
    if (!context) {
      const folder = path.join(this.home,'profiles',id);
      const browsers = process.platform === 'win32' ? ['chrome','msedge'] : ['chrome'];
      let last;
      for (const channel of browsers) {
        try { context = await this.chromium().launchPersistentContext(folder,{ channel, headless:false, viewport:null }); break; }
        catch (e) { last=e; }
      }
      if (!context) throw new Error('请安装 Chrome 或 Edge 后重试：' + safeError(last));
      this.owned.set(id,context); context.on('close',()=>this.owned.delete(id));
    }
    let page = context.pages().find(p=>p.url().startsWith('https://chatgpt.com/'));
    if (!page) { page = await context.newPage(); await page.goto('https://chatgpt.com/'); }
    return page;
  }
  async open(id) { const page = await this.ownedPage(id); await page.bringToFront(); return true; }
  async pageFor(binding) {
    if (binding.owned) return this.ownedPage(binding.account_id);
    const browser = await this.browser(binding.endpoint);
    for (const context of browser.contexts()) for (const page of context.pages()) {
      if (!page.url().startsWith('https://chatgpt.com/')) continue;
      const cdp = await context.newCDPSession(page);
      try { if ((await cdp.send('Target.getTargetInfo')).targetInfo.targetId === binding.target_id) return page; }
      finally { await cdp.detach(); }
    }
    throw new Error('绑定的 Work 标签页已关闭，请重新连接');
  }
  async identity(page) {
    if (!page.url().startsWith('https://chatgpt.com/')) throw new Error('请打开 ChatGPT Work 页面');
    return page.evaluate(pageIdentity);
  }
  async bind(input, account) {
    requireID(input.account_id);
    if (!account?.email || account.disabled) throw new Error('请选择有邮箱且已启用的账号');
    const b = input.owned ? { owned:true, account_id:input.account_id } : { endpoint:endpoint(input.endpoint), target_id:input.target_id };
    if (!b.owned && !/^[A-Fa-f0-9]{32}$/.test(b.target_id || '')) throw new Error('请选择一个 ChatGPT 标签页');
    if (this.busy.has(this.bindingKey(b))) throw new Error('此 Work 页面正在运行');
    const page = await this.pageFor(b); const identity = await this.identity(page);
    if (identity.email !== account.email.toLowerCase().trim()) throw new Error('网页登录邮箱与所选账号不一致，未绑定');
    if (!await page.getByRole('textbox',{ name:/ChatGPT Work/ }).count()) throw new Error('请先在 ChatGPT 选择“工作”并打开 Work 输入框，再绑定');
    const catalog = await page.evaluate(pageBackend,'/backend-api/tpp/models/?iim=false&include_icons=false');
    const models = catalog.status === 200 ? (catalog.data.models || []).filter(m => workModelAllowed(m.slug)).map(m => ({ slug:m.slug, title:m.title, efforts:(m.thinking_efforts || []).map(e => e.thinking_effort) })) : [];
    if (!models.length) throw new Error('此工作区没有可用的 ChatGPT Work 模型');
    this.bindings[input.account_id] = { ...b, workspace:identity.workspace, models, fingerprint:createHash('sha256').update(identity.email+'\n'+identity.workspace).digest('hex') };
    atomicJSON(this.file,this.bindings); return this.snapshot();
  }
  unbind(id) {
    requireID(id); const b=this.bindings[id];
    if (b && this.busy.has(this.bindingKey(b))) throw new Error('Work 请求尚未结束');
    delete this.bindings[id]; atomicJSON(this.file,this.bindings); return this.snapshot();
  }
  async artifacts(page, text, cid, mid) {
    const files = [...new Set([...text.matchAll(/\]\(sandbox:(\/[^)\n]+)\)/g)].map(m=>m[1]))];
    if (files.length > 4) throw new Error('Work 文件超过 4 个，尚不支持');
    for (const file of files) {
      const ext = path.extname(file).toLowerCase();
      if (!['.html','.svg','.txt','.md','.json','.csv','.py','.js','.css'].includes(ext)) throw new Error('Work 当前不支持二进制附件');
      const content = await page.evaluate(async ({ cid,mid,file }) => {
        const s = await (await fetch('/api/auth/session')).json();
        const query = new URLSearchParams({ download_intent:'false', message_id:mid, sandbox_path:file });
        const r = await fetch('/backend-api/conversation/'+cid+'/interpreter/download?'+query,{ headers:{ authorization:'Bearer '+s.accessToken } });
        if (!r.ok) throw new Error('文件信息读取失败：'+r.status);
        const d=await r.json(); const u = new URL(d.download_url || d.url,location.origin);
        if (u.origin !== location.origin || !u.pathname.startsWith('/backend-api/estuary/content')) throw new Error('文件下载地址不匹配');
        const f=await fetch(u); if (!f.ok) throw new Error('文件下载失败：'+f.status);
        const text=await f.text(); if (new TextEncoder().encode(text).length>1000000) throw new Error('文件超过 1 MB'); return text;
      },{ cid,mid,file });
      const fence = '`'.repeat(Math.max(3,...[...content.matchAll(/`+/g)].map(m=>m[0].length+1)));
      text += '\n\n[Work generated file: '+path.basename(file)+']\n'+fence+ext.slice(1)+'\n'+content+'\n'+fence;
    }
    return text;
  }
  async turn(page, binding, plan, signal) {
    let draft; let submitted=false;
    const model=plan.model, effort=plan.upstreamEffort;
    const check = () => { if (signal.aborted && !submitted) throw new Error('请求已取消'); };
    try {
      check();
      if (await page.getByRole('button',{ name:/^(停止|Stop generating)$/ }).count()) throw new Error('Work 页面已有任务，请等待完成');
      const existing = page.getByRole('textbox',{ name:/ChatGPT Work/ });
      if (await existing.count() && (await existing.innerText()).trim()) throw new Error('Work 页面有未发送草稿，请先处理');
      await page.getByRole('button',{ name:/^(新聊天|New chat)$/ }).first().click({ timeout:15000 });
      const editor=page.getByRole('textbox',{ name:/ChatGPT Work/ }); await editor.waitFor({ timeout:15000 });
      const identity=await this.identity(page);
      if (createHash('sha256').update(identity.email+'\n'+identity.workspace).digest('hex')!==binding.fingerprint) throw new Error('Work 登录身份或工作区已改变');
      let imageBase = 0;
      if (plan.images?.length) {
        imageBase = await page.evaluate(composerImages);
        await page.evaluate(pasteImages,{ images:plan.images });
        let attached = false;
        for (let i=0;i<40;i++) {
          check();
          if (await page.evaluate(composerImages) >= imageBase + plan.images.length) { attached = true; break; }
          await pause(500);
        }
        if (!attached) throw new Error('Work 网页没有接受图像附件，请重试');
      }
      await page.evaluate(capture,{ prompt:plan.prompt, model, effort, identity });
      await editor.fill(plan.prompt); draft={ editor, text:await editor.textContent() };
      if (plan.images?.length && await page.evaluate(composerImages) < imageBase + plan.images.length) throw new Error('Work 网页在填写文字后丢失了图像附件，已取消发送');
      let prepared;
      for (let i=0;i<45;i++) {
        check();
        prepared=await page.evaluate(prompt=>[...(window.__wishWork?.traffic || [])].reverse().find(r=>{ const parts=r.body?.partial_query?.content?.parts; return r.path.endsWith('/prepare') && r.status===200 && Array.isArray(parts) && parts.includes(prompt); }),plan.prompt);
        if (prepared) break; await pause(400);
      }
      if (!prepared) throw new Error('Work 没有成功准备完整提示词，未发送');
      if (prepared.body.model!==model || prepared.body.thinking_effort!==effort || prepared.body.conversation_origin!=='tpp') throw new Error(`请在 Work 网页选择 ${modelTitle(model)} / ${EFFORT_LABELS[plan.clientEffort] || plan.clientEffort}，再重试`);
      check(); await page.getByRole('button',{ name:/^(发送|Send prompt|Send)$/ }).click({ timeout:15000 }); submitted=true;
      const start=Date.now(); let lastPoll=0;
      while (Date.now()-start < 300000) {
        const send=await page.evaluate(()=>window.__wishWork?.traffic.find(r=>r.path==='/backend-api/f/conversation'));
        if (send?.status>=400) throw new Error('Work 上游返回 HTTP '+send.status);
        const cid=page.url().match(/\/c\/([0-9a-f-]{36})$/)?.[1];
        if (cid && Date.now()-lastPoll>=4000) {
          lastPoll=Date.now(); const r=await page.evaluate(pageBackend,'/backend-api/conversation/'+cid);
          if (r.status===200) {
            const mapping=r.data.mapping || {}; const msg=mapping[r.data.current_node]?.message;
            if (msg?.author?.role==='assistant' && msg.end_turn===true && msg.status==='finished_successfully' && msg.channel==='final') {
              if (msg.metadata?.model_slug!==model || msg.metadata?.thinking_effort!==effort || send?.status!==200) throw new Error('Work 返回模型、档位或发送凭据不匹配');
              const inputs=Object.values(mapping).filter(n=>n.message?.author?.role==='user').map(n=>n.message.content?.parts);
              if (inputs.length!==1 || !Array.isArray(inputs[0]) || !inputs[0].includes(plan.prompt)) throw new Error('Work 会话内容不匹配，拒绝旧回答');
              const identity=await this.identity(page);
              if (createHash('sha256').update(identity.email+'\n'+identity.workspace).digest('hex')!==binding.fingerprint) throw new Error('Work 登录身份或工作区已改变');
              if (signal.aborted) throw new Error('请求已取消；已等待上游结束，未重发');
              let text=(msg.content?.parts || []).filter(v=>typeof v==='string').join('');
              text=await this.artifacts(page,text,cid,msg.id);
              return responseFor(text,plan.catalog,{ model:model, model_field_source:'upstream_message_metadata', client_effort:plan.clientEffort, upstream_effort:effort });
            }
          } else if (r.status!==404) throw new Error('Work 读取结果失败：HTTP '+r.status);
        }
        await pause(500);
      }
      throw new Error('Work 请求超过 300 秒；未重发，请检查浏览器中的任务');
    } finally {
      if (!submitted && draft) try { if (await draft.editor.textContent()===draft.text) await draft.editor.fill(''); } catch {}
      try { await page.evaluate(()=>{ if (window.__wishWork) { window.fetch=window.__wishWork.original; delete window.__wishWork; } }); } catch {}
    }
  }
  async execute(envelope, signal) {
    const plan=planRequest(envelope.request); const binding=this.bindings[requireID(envelope.account_id)];
    if (!binding) throw new Error('此账号尚未连接 Work；请在 Work 通道设置中登录并绑定');
    if (Array.isArray(binding.models) && binding.models.length && !binding.models.some(m => m.slug === plan.model)) throw new Error(`此工作区没有 ${modelTitle(plan.model)}，请重新绑定后在 Work 网页选择受支持的模型`);
    const key=this.bindingKey(binding); if (this.busy.has(key)) throw new Error('Work 页面正在处理请求，请等待完成');
    this.busy.add(key);
    try {
      const page=await this.pageFor(binding); const identity=await this.identity(page);
      if (identity.email!==String(envelope.email).toLowerCase().trim() || createHash('sha256').update(identity.email+'\n'+identity.workspace).digest('hex')!==binding.fingerprint) throw new Error('网页身份或工作区与绑定不一致，请重新连接');
      if (envelope.workspace && identity.workspace!==envelope.workspace) throw new Error('网页工作区与导入账号不一致，请在网页切换到该账号所属工作区');
      return await this.turn(page,binding,plan,signal);
    } finally { this.busy.delete(key); }
  }
  async listen() {
    this.server=http.createServer(async(req,res)=>{
      const supplied=Buffer.from(req.headers.authorization || ''), expected=Buffer.from('Bearer '+this.key);
      if (req.method!=='POST' || req.url!=='/responses' || req.headers.origin || req.headers.host!==`127.0.0.1:${this.server.address().port}` || supplied.length!==expected.length || !timingSafeEqual(supplied,expected)) { res.writeHead(401).end(); return; }
      const controller=new AbortController(); req.on('aborted',()=>controller.abort()); res.on('close',()=>{ if (!res.writableEnded) controller.abort(); });
      let timer;
      const event=data=>{ if (!res.destroyed) res.write('event: '+data.type+'\ndata: '+JSON.stringify(data)+'\n\n'); };
      try {
        const chunks=[]; let size=0;
        for await (const chunk of req) { size+=chunk.length; if(size>24*1024*1024) throw new Error('Work 请求体超过 24 MiB'); chunks.push(chunk); }
        const body=JSON.parse(Buffer.concat(chunks).toString('utf8'));
        planRequest(body.request);
        res.writeHead(200,{ 'Content-Type':'text/event-stream', 'Cache-Control':'no-store' });
        res.write(': waiting for Work\n\n'); timer=setInterval(()=>{ if(!res.destroyed)res.write(': waiting for Work\n\n'); },10000);
        const result=await this.execute(body,controller.signal);
        for(const row of responseEvents(result))event(row);
      } catch(e) {
        if (!res.headersSent) { res.writeHead(400,{ 'Content-Type':'application/json' }); res.write(JSON.stringify({ error:{ message:safeError(e) } })); }
        else event({ type:'response.failed', response:{ id:'resp_failed', object:'response', status:'failed', output:[], error:{ type:'work_error', message:safeError(e) } } });
      } finally { clearInterval(timer); res.end(); }
    });
    await new Promise((resolve,reject)=>{this.server.once('error',reject);this.server.listen(0,'127.0.0.1',resolve);});
    this.url=`http://127.0.0.1:${this.server.address().port}`;
  }
  async close() { this.server?.closeAllConnections(); await new Promise(r=>this.server ? this.server.close(r) : r()); for(const context of this.owned.values())await context.close(); for(const browser of this.connections.values())await browser.close(); }
}
module.exports={ Work, endpoint, pageIdentity };
