'use strict';

const { workModelAllowed, workEffortAllowed } = require('./work-protocol.cjs');
const accountID = /^acc-[a-zA-Z0-9-]{1,80}$/;
const PRISM_MODELS = new Set(['gpt-6.1-sol', 'gpt-5.6-sol', 'gpt-5.6-terra', 'gpt-6-luna']);
const PRISM_EFFORTS = new Set(['low', 'medium', 'high', 'max', 'ultra']);
function cleanChannel(value) {
  const channel = value ?? 'bps';
  if (!['bps', 'codex', 'work', 'prism', 'kilo'].includes(channel)) throw new Error('请选择 BPS、原生、Work、Prism 或 Kilo 通道');
  return channel;
}
function prismURL(value) {
  if (typeof value !== 'string' || !value.trim() || value.length > 2048) throw new Error('请填写本机 prism-bridge 地址');
  let url;
  try { url = new URL(value.trim()); } catch { throw new Error('Prism 桥地址格式无效'); }
  if (url.protocol !== 'http:' || !['127.0.0.1', 'localhost', '[::1]'].includes(url.hostname) || !url.port || url.username || url.password || url.search || url.hash || !['', '/', '/v1'].includes(url.pathname)) throw new Error('Prism 桥地址必须是本机 http://127.0.0.1:端口');
  return url.origin;
}
function requireID(id) {
  if (typeof id !== 'string' || !accountID.test(id)) throw new Error('账号标识无效');
  return id;
}
function cleanSettings(input) {
  if (!input || typeof input !== 'object' || Array.isArray(input)) throw new Error('设置无效');
  const out = {};
  if ('bps_transport' in input) {
    if (!['http', 'websocket'].includes(input.bps_transport)) throw new Error('请选择 HTTP 或 WebSocket 传输');
    out.bps_transport = input.bps_transport;
  }
  for (const k of ['auto_refresh', 'usage_probe']) {
    if (k in input) {
      if (typeof input[k] !== 'boolean') throw new Error('设置类型无效');
      out[k] = input[k];
    }
  }
  if ('proxy_url' in input) {
    if (typeof input.proxy_url !== 'string' || input.proxy_url.length > 2048) throw new Error('代理地址无效');
    out.proxy_url = input.proxy_url.trim();
  }
  return out;
}
function cleanLaunch(input) {
  if (!input || typeof input !== 'object') throw new Error('启动参数无效');
  requireID(input.account_id);
  const target = input.target || 'cli';
  if (!['cli', 'app'].includes(target)) throw new Error('启动目标无效');
  if (target === 'cli' && (typeof input.directory !== 'string' || !input.directory.trim() || input.directory.length > 4096)) throw new Error('请选择项目目录');
  if (typeof input.model !== 'string' || !/^[a-zA-Z0-9.-]{1,80}$/.test(input.model)) throw new Error('模型无效');
  if (!['low', 'medium', 'high', 'xhigh', 'max', 'ultra'].includes(input.effort)) throw new Error('推理档位无效');
  const channel = cleanChannel(input.channel), speed = input.speed ?? 'standard';
  if (channel === 'kilo' && speed !== 'standard') throw new Error('Kilo 订阅通道使用标准速度');
  if (!['standard', 'fast'].includes(speed)) throw new Error('请选择标准或快速模式');
  if (channel === 'bps' && speed !== 'standard') throw new Error('BPS 暂不支持快速模式，请选择标准速度或原生通道');
  if (channel === 'work' && (!workModelAllowed(input.model) || !workEffortAllowed(input.effort) || speed !== 'standard')) throw new Error('Work 请选择受支持的 Work 模型与推理档位，并使用标准速度');
  if (channel === 'prism' && (!PRISM_MODELS.has(input.model) || !PRISM_EFFORTS.has(input.effort) || speed !== 'standard')) throw new Error('Prism 请选择受支持的模型与推理档位，并使用标准速度');
  if (channel !== 'work' && channel !== 'prism' && input.effort === 'max') throw new Error('仅 Work / Prism 通道支持 max 推理档位');
  if (channel !== 'prism' && input.effort === 'ultra') throw new Error('仅 Prism 通道支持 ultra 推理档位');
  const out = { account_id: input.account_id, directory: target === 'app' ? '' : input.directory, model: input.model, effort: input.effort, resume: target === 'cli' && input.resume === true, target, channel, speed };
  if (channel === 'prism') {
    out.prism_url = prismURL(input.prism_url);
    if (input.prism_key != null) {
      if (typeof input.prism_key !== 'string' || input.prism_key.length > 512) throw new Error('Prism 密钥无效');
      if (input.prism_key.trim()) out.prism_key = input.prism_key.trim();
    }
  }
  if (target === 'app') {
    out.app_mode = input.app_mode ?? (['prism','kilo'].includes(channel) ? 'isolated' : 'main');
    if (channel === 'kilo' && out.app_mode !== 'isolated') throw new Error('Kilo 请使用独立实例');
    if (!['main', 'isolated'].includes(out.app_mode)) throw new Error('请选择主应用或独立实例');
    if (channel === 'prism' && out.app_mode !== 'isolated') throw new Error('Prism 请使用独立实例，不能接管主 Codex 配置');
  }
  for (const k of ['context_window', 'compact_limit']) {
    if (input[k] != null) {
      if (!Number.isSafeInteger(input[k])) throw new Error('上下文设置必须为整数');
      out[k] = input[k];
    }
  }
  return out;
}
function safeError(error) {
  return String(error?.message || error || '操作失败').replace(/eyJ[a-zA-Z0-9_-]+\.[a-zA-Z0-9_-]+\.[a-zA-Z0-9_-]+/g, '[凭据已隐藏]').slice(0, 1200);
}
module.exports = { requireID, cleanSettings, cleanLaunch, cleanChannel, prismURL, safeError };
