'use strict';
const { randomUUID } = require('node:crypto');
// ChatGPT Work exposes these models and five thinking tiers. The browser sends
// upstream effort names; Codex clients use the low..max vocabulary.
const MODEL = 'gpt-6-astra-wm';
const EFFORT = 'standard';
const MODELS = {
  'gpt-6.1-sol-wm': { title:'GPT-6.1 Sol', default:'medium' },
  'gpt-6-astra-wm': { title:'GPT-6 Astra', default:'low' },
  'gpt-6-sol-wm': { title:'GPT-6 Sol', default:'medium' },
  'gpt-6-luna-wm': { title:'GPT-6 Luna', default:'medium' },
  'gpt-5.6-sol-wm': { title:'GPT-5.6 Sol', default:'low' },
  'gpt-5.6-terra-wm': { title:'GPT-5.6 Terra', default:'medium' },
  'gpt-5.6-luna-wm': { title:'GPT-5.6 Luna', default:'medium' },
};
const EFFORTS = { low:'min', medium:'standard', high:'extended', xhigh:'xhigh', max:'max' };
const EFFORT_LABELS = { low:'快速思考', medium:'标准思考', high:'深入思考', xhigh:'超高思考', max:'最大思考' };
function modelTitle(slug) { return MODELS[slug]?.title || slug; }
function workModelAllowed(slug) { return Object.prototype.hasOwnProperty.call(MODELS, slug); }
function workEffortAllowed(effort) { return Object.prototype.hasOwnProperty.call(EFFORTS, effort); }
const MAX_IMAGES = 4;
const MAX_IMAGE_BYTES = 6 * 1024 * 1024;
const MAX_TOTAL_IMAGE_BYTES = 16 * 1024 * 1024;
function imageData(part) {
  let url = part?.image_url;
  if (url && typeof url === 'object') url = url.url;
  if (typeof url !== 'string' || !url) {
    if (part?.file_id) throw new Error('Work 暂不支持 file_id 图像，请直接粘贴图像');
    throw new Error('Work 图像缺少 image_url');
  }
  if (/^https:\/\//i.test(url)) throw new Error('Work 暂不支持远程图像 URL，请直接粘贴图像');
  const match = url.match(/^data:(image\/(?:png|jpeg|jpg|webp|gif));base64,([A-Za-z0-9+/=]+)$/i);
  if (!match) throw new Error('Work 仅支持粘贴 PNG / JPEG / WebP / GIF 图像');
  const bytes = Math.floor(match[2].length * 3 / 4);
  if (bytes > MAX_IMAGE_BYTES) throw new Error('单张图像超过 6 MB');
  return { dataUrl:url, mime:match[1].toLowerCase(), bytes };
}
function messageSegments(value) {
  if (typeof value === 'string') return [{ type:'text', text:value }];
  if (!Array.isArray(value)) throw new Error('消息内容无效');
  const segments = [];
  for (const part of value) {
    if (!part || typeof part !== 'object') throw new Error('消息内容无效');
    if (['input_text','output_text','text'].includes(part.type) && typeof part.text === 'string') segments.push({ type:'text', text:part.text });
    else if (part.type === 'input_image') segments.push({ type:'image', ...imageData(part) });
    else throw new Error('Work 不支持此消息内容：' + String(part.type || 'unknown'));
  }
  return segments;
}
function toolOutput(value) {
  if (!Array.isArray(value)) return value;
  return value.map(part => {
    if (part && part.type === 'input_image') throw new Error('Work 暂不支持工具结果中的图像；未丢弃图像，请选择支持工具图像的通道');
    if (part && typeof part.text === 'string') return part.text;
    return JSON.stringify(part);
  }).join('');
}
function planRequest(body) {
  if (!body || typeof body !== 'object' || Array.isArray(body)) throw new Error('请求必须是 JSON 对象');
  if (!workModelAllowed(body.model)) throw new Error('Work 请选择受支持的 Work 模型');
  const clientEffort = body.reasoning?.effort || body.reasoning_effort || 'medium';
  if (!workEffortAllowed(clientEffort)) throw new Error('Work 推理档位无效，请选择 low / medium / high / xhigh / max');
  if (body.previous_response_id) throw new Error('Work 需要客户端回传完整历史，不支持 previous_response_id');
  if (body.tool_choice && body.tool_choice !== 'auto') throw new Error('Work 仅支持 tool_choice=auto');
  if (body.service_tier && !['auto','default'].includes(body.service_tier)) throw new Error('Work 不支持快速服务等级');
  for (const key of ['temperature','top_p','seed','max_tokens','max_output_tokens','response_format','background','truncation']) if (key in body) throw new Error('Work 尚不支持参数：' + key);
  if (body.include && (!Array.isArray(body.include) || body.include.some(v => v !== 'reasoning.encrypted_content'))) throw new Error('Work 不支持此 include 参数');
  if (!Array.isArray(body.tools || [])) throw new Error('tools 必须是数组');
  const catalog = (body.tools || []).map(t => {
    if (t?.type === 'custom') {
      if (!/^[a-zA-Z0-9_.-]{1,128}$/.test(t.name || '')) throw new Error('工具名称无效');
      const entry = { name:t.name, type:'custom', parameters:{ type:'object', properties:{ input:{ type:'string', description:'Exact unmodified tool input text; do not wrap it in JSON.' } }, required:['input'], additionalProperties:false } };
      for (const key of ['description','format']) if (t[key] != null) entry[key] = t[key];
      return entry;
    }
    if (t?.type !== 'function') throw new Error('Work 仅支持 JSON 函数工具和带名称的自由格式客户端工具，不支持内置工具');
    const f = t.function || t;
    if (!/^[a-zA-Z0-9_.-]{1,128}$/.test(f.name || '')) throw new Error('工具名称无效');
    return { ...Object.fromEntries(['name','description','parameters'].filter(k => k in f).map(k => [k,f[k]])), type:'function' };
  });
  if (new Set(catalog.map(t => t.name)).size !== catalog.length) throw new Error('工具名称重复');
  let history = body.input ?? body.messages;
  if (typeof history === 'string') history = [{ role:'user', content:history }];
  if (!Array.isArray(history) || !history.length) throw new Error('需要完整的 input/messages 历史');
  const normalized = history.map(item => {
    if (!item || typeof item !== 'object') throw new Error('历史消息无效');
    if (!item.type || item.type === 'message') {
      if (!['user','assistant','system','developer','tool'].includes(item.role)) throw new Error('消息角色无效');
      return { kind:'message', role:item.role, segments:messageSegments(item.content ?? ''), extra:Object.fromEntries(['tool_calls','tool_call_id','name'].filter(k => k in item).map(k => [k,item[k]])) };
    }
    if (['function_call','function_call_output','custom_tool_call','custom_tool_call_output'].includes(item.type)) {
      const row = Object.fromEntries(['type','call_id','name','arguments','input','output'].filter(k => k in item).map(k => [k,item[k]]));
      if (item.type === 'custom_tool_call' && (!row.name || typeof row.input !== 'string')) throw new Error('自定义工具历史无效');
      if (item.type === 'custom_tool_call_output' && row.call_id == null) throw new Error('自定义工具结果缺少 call_id');
      if ('output' in row) row.output = toolOutput(row.output);
      return { kind:'tool', row };
    }
    throw new Error('Work 不支持的历史类型：' + item.type);
  });
  const allImages = [];
  for (const entry of normalized) if (entry.kind === 'message') for (const segment of entry.segments) if (segment.type === 'image') { segment.index = allImages.length; allImages.push(segment); }
  if (allImages.length > MAX_IMAGES) throw new Error('Work 完整历史最多支持 4 张图像；未截断，请先整理会话');
  const attached = [];
  let totalBytes = 0;
  for (let index = 0; index < allImages.length; index++) {
    const image = allImages[index];
    totalBytes += image.bytes;
    if (totalBytes > MAX_TOTAL_IMAGE_BYTES) throw new Error('图像总大小超过 16 MB');
    image.attached = attached.length + 1;
    attached.push(image.dataUrl);
  }
  const entries = normalized.map(entry => {
    if (entry.kind === 'tool') return entry.row;
    const content = entry.segments.map(segment => segment.type === 'text' ? segment.text : (segment.attached ? `[image #${segment.attached} attached]` : '[image omitted: only the latest 4 images are attached]')).join('');
    return { role:entry.role, content, ...entry.extra };
  });
  let prompt;
  if (!catalog.length && !body.instructions && entries.length === 1 && entries[0].role === 'user') prompt = entries[0].content;
  else prompt = 'You are answering through a text bridge to an external client. The JSON below is the complete client conversation; continue its last turn. Preserve the user task. Client operations run on the client computer, not in your cloud sandbox. Images marked [image #N attached] are attached to this message in that order; images marked omitted are not attached. To request one client operation, return ONLY this XML block and wait for its actual result:\n<client_tool_call>{"name":"EXACT_CATALOG_NAME","arguments":{}}</client_tool_call>\nUse the exact JSON schema. Tools declared with type "custom" take exactly one string argument: {"input":"EXACT UNMODIFIED TOOL INPUT"}. function_call_output, custom_tool_call_output and tool messages are actual client results. Never invent those results or execute these operations with cloud tools. When the task is complete, return the final answer as ordinary text.\n' + JSON.stringify({ instructions:body.instructions || '', client_tools:catalog, conversation:entries });
  if (Buffer.byteLength(prompt) > 180000) throw new Error('Work 输入超过 180000 UTF-8 字节；未截断，请先压缩历史');
  return { prompt, catalog, model:body.model, clientEffort, upstreamEffort:EFFORTS[clientEffort], images:attached };
}
function parseAnswer(text, catalog) {
  const match = text.match(/^\s*<client_tool_call>\s*([\s\S]*?)\s*(?:<\/client_tool_call>)?\s*$/);
  if (match) {
    // A complete JSON object may arrive without the closing marker. Never repair JSON.
    const call = JSON.parse(match[1]);
    const tool = call && catalog.find(t => t.name === call.name);
    if (!tool || !call.arguments || typeof call.arguments !== 'object' || Array.isArray(call.arguments)) throw new Error('模型返回了未声明的工具或无效参数；未执行');
    const callID = 'call_'+randomUUID();
    if (tool.type === 'custom') {
      if (Object.keys(call.arguments).length !== 1 || typeof call.arguments.input !== 'string') throw new Error('自定义工具调用必须只包含字符串 input 参数；未执行');
      return [{ id:'ctc_'+randomUUID(), type:'custom_tool_call', status:'completed', call_id:callID, name:call.name, input:call.arguments.input }];
    }
    return [{ id:'fc_'+randomUUID(), type:'function_call', status:'completed', call_id:callID, name:call.name, arguments:JSON.stringify(call.arguments) }];
  }
  if (text.includes('<client_tool_call')) throw new Error('工具调用格式不完整或混有其他文本；未执行');
  if (!text.trim()) throw new Error('Work 返回空文本');
  return [{ id:'msg_'+randomUUID(), type:'message', role:'assistant', status:'completed', content:[{ type:'output_text', text, annotations:[] }] }];
}
function responseFor(text, catalog, metadata = {}) {
  return { id:'resp_'+randomUUID(), object:'response', created_at:Math.floor(Date.now()/1000), status:'completed', model:metadata.model || MODEL, output:parseAnswer(text,catalog), usage:null, error:null, metadata:{ route:'chatgpt_work_tpp', model_fallback:false, client_effort:'medium', upstream_effort:EFFORT, tool_transport:'text_dispatcher_v1', incremental_text_stream:false, encrypted_reasoning_available:false, ...metadata } };
}
function responseEvents(response) {
  const rows = []; const emit = (type, data) => rows.push({ type, sequence_number:rows.length, ...data });
  const pending = { ...response, status:'in_progress', output:[] };
  emit('response.created',{ response:pending }); emit('response.in_progress',{ response:pending });
  response.output.forEach((item, output_index) => {
    const message = item.type === 'message';
    const field = item.type === 'custom_tool_call' ? 'input' : 'arguments';
    const prefix = item.type === 'custom_tool_call' ? 'response.custom_tool_call_input' : 'response.function_call_arguments';
    emit('response.output_item.added',{ output_index, item:{ ...item, status:'in_progress', ...(message ? { content:[] } : { [field]:'' }) } });
    const shared = { output_index, item_id:item.id };
    if (message) {
      const part = item.content[0];
      emit('response.content_part.added',{ ...shared, content_index:0, part:{ ...part, text:'' } });
      emit('response.output_text.delta',{ ...shared, content_index:0, delta:part.text });
      emit('response.output_text.done',{ ...shared, content_index:0, text:part.text });
      emit('response.content_part.done',{ ...shared, content_index:0, part });
    } else {
      emit(prefix+'.delta',{ ...shared, delta:item[field] });
      emit(prefix+'.done',{ ...shared, [field]:item[field] });
    }
    emit('response.output_item.done',{ output_index, item });
  });
  emit('response.completed',{ response }); return rows;
}
module.exports = { MODEL, EFFORT, MODELS, EFFORTS, EFFORT_LABELS, modelTitle, workModelAllowed, workEffortAllowed, planRequest, parseAnswer, responseFor, responseEvents };
