'use strict';
let autoLoginGeneration = 0, autoLoginID = null, autoLoginTimer;
const authStateText = { queued: '等待中', running: '授权中', waiting: '待处理', completed: '已就绪', failed: '未完成', cancelled: '已取消' };
function loginDialog() {
  const generation = ++autoLoginGeneration;
  $('modal').classList.add('auto-login-dialog');
  modal(`<h2 id="auto-login-title">添加授权账号</h2>
    <div class="login-tabs"><button class="secondary" aria-current="page">账密 + 2FA 导入</button><button id="login-manual" class="secondary">手动浏览器授权</button></div>
    <div id="auto-login-form">
      <label class="field-label" for="auto-login-channel">授权通道</label>
      <select class="full" id="auto-login-channel"><option value="codex">Codex 原生授权</option><option value="bps">BPS 授权</option></select>
      <label class="field-label" for="auto-login-mode">导入方式</label>
      <select class="full" id="auto-login-mode"><option value="protocol">协议授权（首选，不打开浏览器）</option><option value="browser">浏览器自动授权（备用）</option></select>
      <p class="login-hint">每个账号使用独立会话，成功后自动入库。协议失败时显示具体步骤，由你决定是否改用浏览器。</p>
      <div class="login-input-label"><label class="field-label" for="auto-login-input">子号资料 · 每行一个，最多 50 个</label><label><input type="checkbox" id="auto-login-reveal"> 显示输入</label></div>
      <textarea id="auto-login-input" class="login-secret" spellcheck="false" autocomplete="off" autocapitalize="off" aria-describedby="auto-login-format" placeholder="在此粘贴子号资料"></textarea>
      <p id="auto-login-format" class="login-hint">格式：<code>邮箱----密码----2FA种子</code><br>未开启 2FA 时，保留末尾分隔符并留空。密码与种子仅用于本次授权，任务结束即从内存释放。</p>
      <p id="auto-login-error" class="danger-text" role="alert" hidden></p>
    </div>
    <section id="auto-login-results" aria-label="授权进度" hidden>
      <div class="login-summary"><strong id="auto-login-summary" role="status"></strong><span id="auto-login-route"></span></div>
      <progress id="auto-login-progress" aria-label="已处理账号" value="0" max="1"></progress>
      <ol id="auto-login-rows" class="login-queue"></ol>
    </section>
    <div class="modal-actions login-footer"><button class="secondary" id="auto-login-close">取消</button><button class="secondary" id="auto-login-open" hidden>查看授权浏览器</button><button class="secondary" id="auto-login-skip" hidden>跳过当前账号</button><button class="primary" id="auto-login-start">开始授权</button></div>`);
  $('modal').setAttribute('aria-labelledby', 'auto-login-title');
  $('login-manual').onclick = () => { $('auto-login-input').value = ''; autoLoginGeneration++; manualLoginDialog(); };
  $('auto-login-reveal').onchange = () => $('auto-login-input').classList.toggle('login-secret', !$('auto-login-reveal').checked);
  $('auto-login-close').onclick = closeModal;
  $('auto-login-open').onclick = () => action('auto-login-open', () => api.autoLoginOpen(autoLoginID));
  $('auto-login-skip').onclick = () => action('auto-login-skip', () => api.autoLoginSkip(autoLoginID));
  $('auto-login-start').onclick = async () => {
    $('auto-login-start').disabled = true; $('login-manual').disabled = true;
    $('auto-login-error').hidden = true;
    try {
      const result = await api.autoLoginStart({ channel: $('auto-login-channel').value, mode: $('auto-login-mode').value, text: $('auto-login-input').value });
      if (generation !== autoLoginGeneration) { await api.autoLoginCancel(result.id); return; }
      $('auto-login-input').value = ''; autoLoginID = result.id;
      $('auto-login-form').hidden = true; $('auto-login-start').hidden = true; $('auto-login-results').hidden = false;
      const poll = async () => {
        if (generation !== autoLoginGeneration) return;
        try {
          const status = await api.autoLoginStatus(result.id);
          if (generation !== autoLoginGeneration) return;
          renderLoginProgress(status);
          if (status.done) { autoLoginID = null; await refresh(true); return; }
          autoLoginTimer = setTimeout(poll, 800);
        } catch {
          if (generation !== autoLoginGeneration) return;
          $('auto-login-summary').textContent = '读取授权进度失败，可关闭面板取消任务后重试';
          autoLoginTimer = setTimeout(poll, 2000);
        }
      };
      renderLoginProgress(result); void poll();
    } catch (error) {
      if (generation !== autoLoginGeneration) return;
      $('auto-login-error').textContent = error.message; $('auto-login-error').hidden = false;
      $('auto-login-start').disabled = false; $('login-manual').disabled = false;
    }
  };
  $('auto-login-rows').onclick = event => {
    const choose = event.target.closest('[data-auth-choice]');
    if (choose) void action('auto-login-choice', () => api.autoLoginChoose({ id: autoLoginID, index: Number(choose.dataset.index), choice: choose.dataset.authChoice }));
    const use = event.target.closest('[data-auth-use]');
    if (use) void action('auto-login-use', async () => {
      await api.selectAccount(use.dataset.authUse); await api.preferences({ channel: use.dataset.channel });
      state.selected = use.dataset.authUse; launchChannel(use.dataset.channel); await refresh(true); renderAccounts();
      toast('账号与通道已选定，可直接测试或启动 Codex'); closeModal();
    });
  };
  $('auto-login-input').focus();
}
function renderLoginProgress(batch) {
  const done = batch.rows.filter(row => ['completed', 'failed', 'cancelled'].includes(row.status)).length;
  const saved = batch.rows.filter(row => row.status === 'completed').length;
  const title = batch.done ? `处理完成 · ${saved} / ${batch.rows.length} 个授权已保存` : `正在处理 ${Math.min(done + 1, batch.rows.length)} / ${batch.rows.length} · 已保存 ${saved} 个`;
  if ($('auto-login-summary').textContent !== title) $('auto-login-summary').textContent = title;
  $('auto-login-route').textContent = `${batch.channel === 'bps' ? 'BPS' : 'Codex 原生'} · ${batch.mode === 'browser' ? '浏览器备用' : '协议授权'}`;
  $('auto-login-progress').max = batch.rows.length; $('auto-login-progress').value = done;
  $('auto-login-close').textContent = batch.done ? '完成' : '取消全部';
  $('auto-login-open').hidden = batch.done || batch.mode !== 'browser'; $('auto-login-skip').hidden = batch.done;
  $('login-manual').disabled = !batch.done;
  const html = batch.rows.map(row => `<li class="login-queue-row" data-status="${esc(row.status)}"><div class="login-row-heading"><strong>${esc(row.email)}</strong><span class="login-state">${authStateText[row.status]}</span></div><p>${esc(row.message)}</p>${row.workspace ? `<small>工作区：${esc(row.workspace)}</small>` : ''}${row.cleanup_warning ? `<p class="danger-text">${esc(row.cleanup_warning)}</p>` : ''}${row.choices?.length ? `<div class="login-workspaces" aria-label="选择工作区">${row.choices.map(c => `<button class="secondary" data-index="${row.index}" data-auth-choice="${esc(c.id)}">${esc(c.label)}</button>`).join('')}</div>` : ''}${batch.done && row.account_id ? `<button class="secondary login-use" data-auth-use="${esc(row.account_id)}" data-channel="${esc(batch.channel)}">使用此账号 · ${batch.channel === 'bps' ? 'BPS' : '原生'}</button>` : ''}</li>`).join('');
  // Preserve keyboard focus while polling an unchanged workspace choice.
  if ($('auto-login-rows').dataset.html !== html) { $('auto-login-rows').innerHTML = html; $('auto-login-rows').dataset.html = html; }
}
$('modal').addEventListener('close', () => {
  autoLoginGeneration++; clearTimeout(autoLoginTimer);
  const pending = autoLoginID; autoLoginID = null;
  const input = $('auto-login-input'); if (input) input.value = '';
  $('modal').classList.remove('auto-login-dialog'); $('modal').removeAttribute('aria-labelledby');
  if (pending) void api.autoLoginCancel(pending).catch(() => toast('取消授权未完成，请检查授权浏览器', 'error'));
});
