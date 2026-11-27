'use strict';
let primerGeneration = 0, primerTimer;
const primerActive = job => ['catalog', 'running', 'refreshing'].includes(job?.state);
const primerStateLabel = {idle:'尚未预热',catalog:'核对 BPS 模型准入',running:'BPS 预热中',refreshing:'BPS 已完成，续期原生授权',ready:'预热完成 · 后续请求走原生',failed:'预热未完成',cancelled:'已停止',interrupted:'上次任务已中断',stale:'记录已失效，请重新预热'};
let primerSummaryGeneration = 0;
async function refreshBPSNativeSummary() {
  const generation = ++primerSummaryGeneration, summary = $('bps-native-summary');
  summary.hidden = $('channel').value !== 'bps_native';
  if (summary.hidden) return;
  const account = accountByID(state.selected);
  if (account?.oauth_client_id !== 'app_EMoamEEZ73f0CkXaXp7hrann') {
    summary.textContent = '请选择原生 Codex 授权；预热还需要同账号的独立 BPS 授权。'; return;
  }
  const id = account.id;
  try {
    const status = await api.bpsNativeStatus(id);
    if (generation !== primerSummaryGeneration || state.selected !== id) return;
    const job = status.job;
    summary.textContent = job.state === 'idle' ? status.bps_candidates.length ? '两份授权可配对。打开预热面板开始准备。' : '缺少匹配的 BPS 授权，打开面板补充授权。' : `${primerStateLabel[job.state] || job.state} · ${job.completed || 0} / ${job.plan?.length || job.requested}${job.current_model ? ' · ' + job.current_model : ''}`;
    $('bps-native-setup').textContent = primerActive(job) ? '查看后台预热进度' : job.state === 'ready' ? '查看预热完成记录' : '配置 BPS 预热 / 查看进度';
  } catch { if (generation === primerSummaryGeneration) summary.textContent = '暂时无法读取预热状态，请检查本地服务后重试。'; }
}
setInterval(() => { if (state.page === 'accounts' && !$('modal').open) void refreshBPSNativeSummary(); }, 5000);

async function openBPSNative(id = state.selected, launchAfter) {
  let native = accountByID(id);
  if (native?.oauth_client_id === 'app_fnr0pYvVwwFDocDumLG3H2Bp') {
    const matches = (await api.bpsNativeStatus(native.id)).native_candidates;
    if (matches.length === 1) native = accountByID(matches[0]);
  }
  if (!native || native.oauth_client_id !== 'app_EMoamEEZ73f0CkXaXp7hrann') {
    toast('请先选择原生 Codex 授权记录；此流程还需要同账号的独立 BPS 授权。', 'error'); return;
  }
  id = native.id;
  const initial = await api.bpsNativeStatus(id);
  const generation = ++primerGeneration;
  clearTimeout(primerTimer);
  $('modal').classList.add('primer-dialog');
  modal(`<h2 id="primer-title">BPS 预热 → 原生 Codex</h2><p>完成 BPS 预热后，使用同账号的原生授权启动 Codex。</p>
    <ol class="primer-stages" aria-label="通道准备步骤"><li id="primer-stage-auth">核对两份授权</li><li id="primer-stage-warm">完成 BPS 预热</li><li id="primer-stage-native">原生通道就绪</li></ol>
    <dl class="primer-identity"><dt>原生账号</dt><dd>${esc(label(native))}</dd><dt>工作区</dt><dd>${esc(native.account_id)}</dd></dl>
    <label class="field-label" for="primer-bps">同账号 BPS 授权</label><select id="primer-bps" class="full"></select>
    <button id="primer-authorize" class="text-button">补充 BPS 授权</button>
    <label class="field-label" for="primer-total">预热请求总数（至少覆盖每个可用模型一次）</label>
    <input id="primer-total" class="full" type="number" min="1" max="100" value="${initial.job.requested || 40}">
    <p class="small">BPS 使用 low 档位，不发送 service_tier；间隔 2–5 秒。会消耗 BPS 额度。正式请求保留你选择的模型、推理强度和速度。效果需通过同账号测智验证。</p>
    <div class="primer-status"><strong id="primer-state" role="status"></strong><span id="primer-count"></span></div><progress id="primer-progress" value="0" max="1" aria-label="BPS 预热完成进度"></progress>
    <p id="primer-detail" class="small"></p><p id="primer-error" class="danger-text" role="alert"></p>
    <details open><summary>各模型完成情况</summary><ul id="primer-models" class="primer-model-list"></ul></details>
    <p class="small">关闭面板不取消任务；可重开查看或停止。中断后只继续未完成部分。</p>
    <div class="modal-actions"><button id="primer-close" class="secondary">关闭</button><button id="primer-stop" class="secondary" hidden>停止</button><button id="primer-restart" class="secondary" hidden>重新预热</button><button id="primer-start" class="primary">开始预热</button><button id="primer-use" class="primary" hidden>${launchAfter ? '完成并启动 Codex' : '使用此通道'}</button></div>`);
  $('modal').setAttribute('aria-labelledby', 'primer-title');
  const select = $('primer-bps');
  select.innerHTML = initial.bps_candidates.map(candidate => `<option value="${esc(candidate)}">${esc(label(accountByID(candidate)))} · BPS 授权</option>`).join('');
  if (initial.bps_candidates.includes(initial.job.bps_account_id)) select.value = initial.job.bps_account_id;
  $('primer-close').onclick = closeModal;
  $('primer-authorize').onclick = () => { closeModal(); loginDialog(); $('auto-login-channel').value = 'bps'; $('auto-login-channel').dispatchEvent(new Event('change')); };
  $('primer-stop').onclick = () => action('primer-stop', async () => { await api.bpsNativeCancel(id); });
  const start = restart => action('primer-start', async () => {
    $('primer-start').disabled = $('primer-restart').disabled = true;
    try { const job = await api.bpsNativeStart({account_id:id,bps_account_id:select.value,total_requests:Number($('primer-total').value),restart}); render(job); }
    catch(error) { if (generation === primerGeneration) { $('primer-error').textContent = error.message; $('primer-start').disabled = !select.value; $('primer-restart').disabled = false; } }
  });
  $('primer-start').onclick = () => start(false);
  $('primer-restart').onclick = () => start(true);
  $('primer-use').onclick = () => {
    state.selected = id; launchChannel('bps_native'); renderAccounts(); closeModal();
    if (launchAfter) void launchAfter(id); else void api.preferences({channel:'bps_native'});
  };
  let lastModel = '';
  const render = job => {
    if (generation !== primerGeneration || !$('primer-state')) return;
    const active = primerActive(job);
    const cooling = Date.parse(job.retry_at) > Date.now();
    const total = job.plan?.length || job.requested || Number($('primer-total').value) || 40;
    $('primer-state').textContent = primerStateLabel[job.state] || job.state;
    $('primer-progress').max = job.plan?.length || Number($('primer-total').value) || 40;
    $('primer-progress').value = job.completed || 0;
    $('primer-count').textContent = `${job.completed || 0} / ${total}`;
    $('primer-stage-auth').dataset.state = select.value ? 'done' : 'current';
    $('primer-stage-warm').dataset.state = job.completed === total ? 'done' : select.value ? 'current' : 'pending';
    $('primer-stage-native').dataset.state = job.state === 'ready' ? 'done' : job.state === 'refreshing' ? 'current' : 'pending';
    $('primer-close').textContent = active ? '后台运行' : '关闭';
    $('primer-detail').textContent = `已完成 ${job.completed || 0} / ${job.plan?.length || job.requested || 0}${job.current_model ? ' · ' + job.current_model : ''}${job.state === 'ready' ? ' · 后续启动复用本次记录，不重复预热' : ''}`;
    $('primer-error').textContent = (job.error || (!select.value ? '缺少同邮箱、用户、工作区和代理的 BPS 授权，请先补充。' : '')) + (job.retry_at && !job.retry_at.startsWith('0001-') ? ` · 下次可尝试时间：${new Date(job.retry_at).toLocaleString('zh-CN')}` : '');
    const models = [...new Set(job.plan || [])], completedModels = (job.plan || []).slice(0, job.completed || 0);
    $('primer-models').innerHTML = models.length ? models.map(model => {
      const done = completedModels.filter(x => x === model).length, planned = job.plan.filter(x => x === model).length;
      const current = job.current_model === model && job.state !== 'ready';
      const status = done === planned ? '已完成' : current ? (active ? '请求中' : '待继续') : '等待';
      return `<li data-current="${current}" ${current ? 'aria-current="step"' : ''}><span>${esc(model)}</span><span>${done} / ${planned}</span><span>${status}</span></li>`;
    }).join('') : '<li>启动后读取此账号的实时模型列表</li>';
    if (job.current_model && job.current_model !== lastModel) {
      const list = $('primer-models'), current = list.querySelector('[data-current=true]');
      if (current) { const row = current.getBoundingClientRect(), box = list.getBoundingClientRect(); if (row.top < box.top || row.bottom > box.bottom) list.scrollTop += row.top - box.top; }
      lastModel = job.current_model;
    }
    $('primer-stop').hidden = !active;
    $('primer-start').hidden = active || job.state === 'ready';
    $('primer-start').textContent = cooling ? `${Math.ceil((Date.parse(job.retry_at) - Date.now()) / 1000)} 秒后可继续` : job.completed ? '继续未完成部分' : '开始预热';
    $('primer-start').disabled = active || cooling || !select.value;
    $('primer-restart').hidden = active || job.state === 'idle';
    $('primer-restart').disabled = cooling || !select.value;
    $('primer-use').hidden = job.state !== 'ready';
    $('primer-total').disabled = select.disabled = active;
    $('primer-authorize').hidden = active;
  };
  render(initial.job);
  const poll = async () => {
    if (generation !== primerGeneration || !$('modal').open || !$('primer-state')) return;
    try { const status = await api.bpsNativeStatus(id); render(status.job); }
    catch(error) { if (generation === primerGeneration && $('primer-error')) $('primer-error').textContent = error.message; }
    if (generation === primerGeneration) primerTimer = setTimeout(poll, 1200);
  };
  primerTimer = setTimeout(poll, 1200);
}
$('bps-native-setup').onclick = () => action('primer-open', () => openBPSNative());
$('modal').addEventListener('close', () => { primerGeneration++; clearTimeout(primerTimer); $('modal').classList.remove('primer-dialog'); $('modal').removeAttribute('aria-labelledby'); void refreshBPSNativeSummary(); });
