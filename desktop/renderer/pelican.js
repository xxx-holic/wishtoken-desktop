'use strict';
const pelicanState = { selected: new Set(), batches: [], batch: '', loading: false, initialized: false, prompts:{} };
const pelicanStatus = { running: '生成中', queued: '排队中', completed: '已完成', failed: '失败', cancelling: '取消中', cancelled: '已取消', interrupted: '已中断' };
function pelicanAccounts() {
  const accounts = state.data?.accounts || [];
  $('pelican-accounts').innerHTML = accounts.map(a => `<label class="pelican-account"><input type="checkbox" value="${esc(a.id)}" ${pelicanState.selected.has(a.id) ? 'checked' : ''} ${a.disabled || (a.expired && !a.has_refresh_token) ? 'disabled' : ''}><span>${esc(label(a))}<small>${esc(planLabel(a))}${a.usage?.primary ? ` · 剩余 ${Math.max(0, 100 - a.usage.primary.used_percent)}%` : ' · 额度未查询'}</small></span></label>`).join('') || '<p>请先导入 Team 子号。</p>';
}
async function loadPelican() {
  if (pelicanState.loading) return;
  pelicanState.loading = true;
  try {
    const data = await api.pelicanHistory(); pelicanState.batches = data.batches;
    pelicanState.prompts={pelican:data.default_prompt,candy:data.candy_prompt};
    if (!pelicanState.initialized) {
      $('pelican-channel').value = state.data.preferences.pelican_channel || 'bps';
      channelModels($('pelican-channel').value, $('pelican-model'), $('model').value);
      $('pelican-effort').value = 'medium'; $('pelican-prompt').value = data.default_prompt;
      pelicanChannelOptions();
      if (!pelicanState.selected.size && state.selected) pelicanState.selected.add(state.selected);
      pelicanState.initialized = true;
    }
    if (!data.batches.some(b => b.id === pelicanState.batch)) pelicanState.batch = data.batches[0]?.id || '';
    pelicanAccounts(); renderPelican();
  } catch (e) { toast(e.message, 'error'); }
  finally { pelicanState.loading = false; }
}
function renderPelican() {
  const batches = pelicanState.batches;
  const running = batches.find(b => ['running', 'cancelling'].includes(b.status));
  $('pelican-start').disabled = !!running || !pelicanState.selected.size;
  $('pelican-cancel').hidden = !running; $('pelican-cancel').disabled = running?.status === 'cancelling';
  const b = batches.find(b => b.id === pelicanState.batch);
  $('pelican-progress').textContent = running ? `${running.items.filter(i => !['queued','running'].includes(i.status)).length} / ${running.items.length} 已结束 · ${pelicanStatus[running.status]}` : b ? `${b.items.filter(i => i.status === 'completed').length} / ${b.items.length} 成功` : '';
  const options = batches.map(b => `<option value="${esc(b.id)}">${date(b.created_at)} · ${b.benchmark==='candy'?'糖果题':'鹈鹕'} · ${channelLabel(b.channel)} · ${esc(b.model)} · ${b.items.length} 个账号 · ${pelicanStatus[b.status]}</option>`).join('');
  if ($('pelican-history').innerHTML !== options) $('pelican-history').innerHTML = options;
  $('pelican-history').value = pelicanState.batch;
  $('pelican-delete').hidden = !b || ['running','cancelling'].includes(b.status);
  $('pelican-retry').hidden = !b || !!running || b.items.every(i => i.status === 'completed');
  $('pelican-description').textContent = b ? `${channelLabel(b.channel)} · 请求 ${b.model} / ${b.effort} · ${b.benchmark==='candy'?'糖果题预期 21；自动识别仅供本题核对，可查看完整原文':'点击预览可放大动画'}；返回模型标识不等于内部路由证明` : '测试完成后，原始结果会显示在这里。';
  const container = $('pelican-results');
  if (container.dataset.batch !== b?.id) { container.replaceChildren(); container.dataset.batch = b?.id || ''; }
  for (const item of b?.items || []) {
    let card = document.getElementById(`pelican-${item.id}`);
    if (!card) { card = document.createElement('article'); card.id = `pelican-${item.id}`; card.className = 'pelican-result'; container.append(card); }
    const signature = JSON.stringify(item); if (card.dataset.signature === signature) continue;
    card.dataset.signature = signature;
    const candy=b.benchmark==='candy';
    const result=candy ? `<div class="candy-verdict ${item.verdict==='fail'?'danger-text':''}"><strong>${{pass:'本题通过',fail:'本题未通过',review:'待人工确认'}[item.verdict] || '待人工确认'}</strong><p>${esc(item.reason)}</p></div><pre class="candy-answer">${esc(item.answer_text)}</pre>` : `<iframe title="${esc(item.name)} 的鹈鹕动画" sandbox="allow-scripts" referrerpolicy="no-referrer" src="${esc(item.preview)}"></iframe>`;
    card.innerHTML = `<header><strong>${esc(item.name)}</strong><span class="tag">${pelicanStatus[item.status]}</span></header>${item.status === 'completed' ? `${result}<div class="pelican-metrics">${(item.duration_ms/1000).toFixed(1)} 秒 · 输出 ${item.usage?.output_tokens ?? '—'} tokens · 推理 ${item.usage?.reasoning_tokens ?? '—'} tokens<br>${channelLabel(item.route || b.channel)} · 返回标识：${esc(item.response_model || '未提供')} / ${esc(b.effort)}</div><footer>${candy?'':`<button class="secondary" data-pelican="preview" data-id="${item.id}">放大预览</button>`}<button class="text-button" data-pelican="source" data-id="${item.id}">原始回答</button><button class="text-button" data-pelican="export" data-id="${item.id}">${candy?'保存回答':'保存 HTML'}</button></footer>` : `<div class="pelican-placeholder"><span>${item.status === 'running' ? '正在等待模型回答…' : pelicanStatus[item.status]}</span><p>${esc(item.error || (item.status === 'running' ? '可能需要数分钟；Work 取消后会等待网页任务结束。' : ''))}</p>${item.has_artifact?`<button class="text-button" data-pelican="source" data-id="${item.id}">查看原始回答</button>`:''}</div>`}`;
  }
}
async function startPelican(input) {
  await action('pelican-start', async () => { $('pelican-start').disabled = true; pelicanState.batch = await api.pelicanStart(input || { account_ids: [...pelicanState.selected], benchmark:$('pelican-benchmark').value, channel: $('pelican-channel').value, model: $('pelican-model').value, effort: $('pelican-effort').value, prompt: $('pelican-prompt').value, concurrency: Number($('pelican-concurrency').value) }); await loadPelican(); });
}
function pelicanChannelOptions() {
  const work=$('pelican-channel').value==='work';
  $('pelican-effort').disabled=work; $('pelican-concurrency').disabled=work; $('pelican-work-note').hidden=!work;
  if(work){$('pelican-effort').value='medium';$('pelican-concurrency').value='1';}
}
$('pelican-benchmark').onchange=()=>{$('pelican-prompt').value=pelicanState.prompts[$('pelican-benchmark').value];$('pelican-prompt').rows=$('pelican-benchmark').value==='candy'?8:3;};
$('pelican-channel').onchange = () => action('pelican-channel', async () => { channelModels($('pelican-channel').value, $('pelican-model'), $('pelican-model').value); pelicanChannelOptions(); await api.preferences({ pelican_channel: $('pelican-channel').value }); });
$('pelican-accounts').onchange = event => { const id = event.target.value; event.target.checked ? pelicanState.selected.add(id) : pelicanState.selected.delete(id); renderPelican(); };
$('pelican-current').onclick = () => { pelicanState.selected = new Set([state.data?.codex.active_account_id || state.selected].filter(Boolean)); pelicanAccounts(); renderPelican(); };
$('pelican-all').onclick = () => { pelicanState.selected = new Set((state.data?.accounts || []).filter(a => !a.disabled && !(a.expired && !a.has_refresh_token)).map(a => a.id)); pelicanAccounts(); renderPelican(); };
$('pelican-start').onclick = () => startPelican();
$('pelican-cancel').onclick = () => action('pelican-cancel', async () => { await api.pelicanCancel(); await loadPelican(); });
$('pelican-history').onchange = () => { pelicanState.batch = $('pelican-history').value; renderPelican(); };
$('pelican-retry').onclick = () => { const b = pelicanState.batches.find(b => b.id === pelicanState.batch); if (b) void startPelican({ ...b, account_ids: b.items.filter(i => i.status !== 'completed').map(i => i.account_id) }); };
$('pelican-delete').onclick = () => {
  const id = pelicanState.batch;
  modal('<h2>删除这轮测试？</h2><p>删除本轮结果和保存的动画，账号凭据不受影响。</p><div class="modal-actions"><button id="keep-pelican" class="secondary">保留</button><button id="remove-pelican" class="primary">删除</button></div>');
  $('keep-pelican').onclick = closeModal;
  $('remove-pelican').onclick = () => action('pelican-delete', async () => { await api.pelicanDelete(id); closeModal(); await loadPelican(); });
};
$('pelican-results').onclick = event => {
  const button = event.target.closest('[data-pelican]'); if (!button) return;
  const { pelican: operation, id } = button.dataset;
  void action(`pelican-${operation}`, async () => {
    if (operation === 'export') { if (await api.pelicanExport(id)) toast('测试结果已保存'); }
    if (operation === 'source') { const raw = await api.pelicanSource(id); modal('<h2>模型原始回复</h2><textarea id="pelican-source" readonly aria-label="模型原始回复"></textarea>'); $('pelican-source').value = raw; }
    if (operation === 'preview') {
      const item = pelicanState.batches.flatMap(b => b.items).find(i => i.id === id); if (!item?.preview) return;
      const iframe = document.createElement('iframe'); iframe.sandbox = 'allow-scripts'; iframe.referrerPolicy = 'no-referrer'; iframe.src = item.preview; iframe.title = '鹈鹕动画预览';
      $('pelican-preview-body').replaceChildren(iframe); $('pelican-preview').showModal();
    }
  });
};
$('pelican-preview').onclose = () => $('pelican-preview-body').replaceChildren();
setInterval(() => { if (state.page === 'pelican' && !$('modal').open && !$('pelican-preview').open) void loadPelican(); }, 2500);
