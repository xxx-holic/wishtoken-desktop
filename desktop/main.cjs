'use strict';
const { app, BrowserWindow, ipcMain, dialog, Tray, Menu, nativeImage, shell, clipboard, nativeTheme } = require('electron');
const fs = require('node:fs');
const fsp = require('node:fs/promises');
const path = require('node:path');
const os = require('node:os');
const { execFileSync } = require('node:child_process');
const { pathToFileURL } = require('node:url');
const { BridgeService, atomicJSON } = require('./lib/service.cjs');
const { requireID, cleanLaunch, cleanSettings, cleanChannel, prismURL, safeError } = require('./lib/policy.cjs');
const appRuntime = require('./lib/codex-app.cjs');
const mainProfile = require('./lib/main-profile.cjs');
const { Pelican, DEFAULT_PROMPT } = require('./lib/pelican.cjs');
const { CANDY_PROMPT } = require('./lib/benchmarks.cjs');
const { Work } = require('./lib/work.cjs');
const { externalLink, businessCode } = require('./lib/links.cjs');
const { loginAction, loginURL } = require('./lib/login.cjs');
const { AutoLogin } = require('./lib/auto-login.cjs');
const { SubscriptionBinding } = require('./lib/subscription-binding.cjs');
const { normalizeChannel } = require('./lib/channel-name.cjs');

const dataHome = process.env.GPTBRIDGE_DESKTOP_HOME || path.join(os.homedir(), '.gptbridge-desktop');
const primaryHome = mainProfile.mainHome();
let switchingApp = false;
app.setPath('userData', path.join(dataHome, 'shell'));
app.setAppUserModelId('app.gptbridge.team');
const lock = app.requestSingleInstanceLock();
if (!lock) app.quit();
let window, tray, service, pelican, work, autoLogin, subscriptionBinding, codexApp, quitting = false, allowQuit = false;
const page = pathToFileURL(path.join(__dirname, 'renderer', 'index.html')).href;
const prefsPath = path.join(dataHome, 'desktop.json');
let prefs = {};
function readPrefs() {
  try { prefs = JSON.parse(fs.readFileSync(prefsPath, 'utf8')); } catch { prefs = {}; }
  if (prefs.channel) prefs.channel = normalizeChannel(prefs.channel);
}
function savePrefs() { atomicJSON(prefsPath, prefs); }
function show() { if (window) { if (window.isMinimized()) window.restore(); window.show(); window.focus(); } }
function setupPath() {
  if (process.platform !== 'darwin') return;
  try {
    const output = execFileSync('/bin/zsh', ['-ilc', 'printf "\\nGPTBRIDGE_PATH=%s\\n" "$PATH"'], { timeout: 5000, encoding: 'utf8' });
    const line = output.split('\n').findLast(line => line.startsWith('GPTBRIDGE_PATH='));
    if (line) process.env.PATH = line.slice('GPTBRIDGE_PATH='.length);
  } catch { process.env.PATH = `/opt/homebrew/bin:/usr/local/bin:${process.env.PATH || '/usr/bin:/bin'}`; }
}
async function requestQuit() {
  if (quitting) return;
  const result = await dialog.showMessageBox(window, { type: 'question', title: '退出 WishToken Desktop', message: '退出将停止本地服务', detail: '正在运行的 Codex 将无法继续请求。只想隐藏窗口，请选择“留在后台”。', buttons: ['留在后台', '退出客户端'], defaultId: 0, cancelId: 0 });
  if (result.response === 0) { window.hide(); return; }
  quitting = true;
  if (prefs.restore_on_exit !== false) {
    try { mainProfile.restoreManaged(dataHome, primaryHome); }
    catch (error) {
      if (!/没有可恢复|不一致|备份不存在|config.toml 不存在/.test(safeError(error))) dialog.showErrorBox('恢复 Codex 配置失败', safeError(error));
    }
  }
  await autoLogin?.close();
  await subscriptionBinding?.close();
  await pelican?.close();
  await service?.stop();
  await work?.close();
  allowQuit = true;
  app.quit();
}
async function importPaths(paths) {
  if (!Array.isArray(paths) || paths.length > 100 || paths.some(file => typeof file !== 'string' || !path.isAbsolute(file) || path.extname(file).toLowerCase() !== '.json')) throw new Error('请选择 JSON 文件，单次最多 100 个');
  const result = { imported: 0, merged: 0, skipped: 0, warnings: [], ids: [], files: paths.length };
  for (const file of paths) {
    try {
      const info = await fsp.stat(file);
      if (!info.isFile() || info.size > 16 * 1024 * 1024) throw new Error('文件超过 16 MB 或不是普通文件');
      const raw = (await fsp.readFile(file, 'utf8')).replace(/^\uFEFF/, '');
      JSON.parse(raw);
      const summary = await service.request('/api/accounts/import', 'POST', raw, 90000);
      for (const key of ['imported', 'merged', 'skipped']) result[key] += summary[key] || 0;
      result.ids.push(...summary.ids);
      result.warnings.push(...summary.warnings.map(w => `${path.basename(file)}：${w}`));
    } catch (err) { result.warnings.push(`${path.basename(file)}：${safeError(err)}`); }
  }
  return result;
}
function validSender(event) { return event.sender === window?.webContents && event.senderFrame === window.webContents.mainFrame && event.senderFrame?.url === page; }
async function confirmMainRestart(restore) {
  const result = await dialog.showMessageBox(window, { type: 'question', title: restore ? '恢复主应用配置' : '切换主 Codex App', message: restore ? '还原接管前的 Codex 配置并重新打开 Codex？' : '切换账号需要重启主 Codex App', detail: restore ? '只还原模型、供应商和目录等接管字段；之后新增的项目、插件、MCP 和服务设置会保留。请先保存正在运行的任务。' : '请先保存正在运行的任务，重启会中断进行中的请求。原有项目、已保存的会话和应用数据目录会继续保留。\n首次切换会备份原配置，可在设置中恢复。', buttons: ['取消', restore ? '还原并打开' : '切换并重启'], defaultId: 0, cancelId: 0 });
  return result.response === 1;
}
async function handle(method, input) {
  switch (method) {
    case 'snapshot': {
      const [status, accounts, codex, models, settings] = await Promise.all(['/api/status', '/api/accounts', '/api/codex/launch', '/api/models', '/api/settings'].map(route => service.request(route)));
      return { status, accounts: accounts.accounts, codex: { ...codex, app: codexApp, main_app: mainProfile.status(dataHome, primaryHome) }, models, settings: { proxy_url: settings.proxy_url, bps_transport: settings.bps_transport, auto_refresh: settings.auto_refresh, usage_probe: settings.usage_probe }, preferences: prefs, platform: process.platform, version: app.getVersion() };
    }
    case 'importFiles': {
      const result = await dialog.showOpenDialog(window, { title: '导入账号 JSON', properties: ['openFile', 'multiSelections'], filters: [{ name: '账号 JSON', extensions: ['json'] }] });
      return result.canceled ? null : importPaths(result.filePaths);
    }
    case 'importPaths': return importPaths(input);
    case 'loginStart':
      if (autoLogin.busy()) throw new Error('自动授权正在进行，请先完成或取消。');
      return loginAction(service, 'start', input);
    case 'autoLoginStart': return autoLogin.start(input);
    case 'autoLoginStatus': return autoLogin.snapshot(input);
    case 'autoLoginCancel': return autoLogin.cancel(input);
    case 'autoLoginSkip': return autoLogin.skip(input);
    case 'autoLoginOpen': return autoLogin.open(input);
    case 'autoLoginChoose': return autoLogin.choose(input);
    case 'subscriptionBindingStart': return subscriptionBinding.start(input);
    case 'subscriptionBindingStatus': return subscriptionBinding.snapshot(input);
    case 'subscriptionBindingCancel': return subscriptionBinding.cancel(input);
    case 'subscriptionBindingOpen': return subscriptionBinding.open(input);
    case 'loginStatus': return loginAction(service, 'status', input);
    case 'fenjueSubscription': return service.request(`/api/fenjue/subscription?account_id=${requireID(input)}`);
    case 'fenjueOpen': await shell.openExternal(Buffer.from("aHR0cHM6Ly9hcHAua2lsby5haS9ieW9r", "base64").toString("utf8")); return { opened:true };
    case 'loginCancel': return loginAction(service, 'cancel', input);
    case 'loginOpen': {
      const pending = await loginAction(service, 'status', input);
      if (!pending.active || pending.done) throw new Error('登录会话已结束，请重新发起授权');
      await shell.openExternal(loginURL(pending));
      return { opened: true };
    }
    case 'importText':
      if (typeof input !== 'string' || Buffer.byteLength(input) > 16 * 1024 * 1024) throw new Error('JSON 内容为空或过大');
      try { JSON.parse(input); } catch { throw new Error('JSON 格式不正确，请检查括号、引号和逗号。'); }
      return service.request('/api/accounts/import', 'POST', input, 90000);
    case 'chooseFolder': {
      const result = await dialog.showOpenDialog(window, { title: '选择 Codex 项目目录', defaultPath: prefs.directory || os.homedir(), properties: ['openDirectory', 'createDirectory'] });
      if (result.canceled) return null;
      prefs.directory = result.filePaths[0]; savePrefs();
      return prefs.directory;
    }
    case 'usage': return service.request(`/api/accounts/${requireID(input)}/usage`, 'POST', {}, 40000);
    case 'account': {
      requireID(input?.id);
      if (input.action === 'delete') return service.request(`/api/accounts/${input.id}`, 'DELETE');
      if (['refresh', 'clear-cooldown'].includes(input.action)) return service.request(`/api/accounts/${input.id}/${input.action}`, 'POST', {}, 95000);
      if (input.action === 'update') {
        const patch = {};
        if (typeof input.name === 'string') patch.name = input.name.slice(0, 100);
        if (typeof input.disabled === 'boolean') patch.disabled = input.disabled;
        return service.request(`/api/accounts/${input.id}`, 'PATCH', patch);
      }
      throw new Error('未知账号操作');
    }
    case 'launch': {
      if (switchingApp) throw new Error('正在切换 Codex，请等待完成');
      const options = cleanLaunch(input);
      switchingApp = true;
      try {
        let processes = [];
        if (options.target === 'app') {
          codexApp = await appRuntime.discoverApp(prefs.app_path);
          if (!codexApp.installed) throw new Error(codexApp.error);
          if (options.app_mode === 'main') {
            processes = await appRuntime.mainProcesses(codexApp.binary);
            if (processes.length && !(await confirmMainRestart(false))) return { cancelled: true };
          }
        }
        const result = await service.request('/api/codex/launch', 'POST', { ...options, prepare_only: options.target === 'app' }, 25000);
        if (options.target === 'app' && options.app_mode === 'main') {
          const input = { dataHome, home: primaryHome, profileHome: result.home, key: service.config.api_key, accountID: options.account_id, channel: options.channel };
          mainProfile.prepare(input); // Validate before closing the existing application.
          await appRuntime.closeMainProcesses(codexApp.binary, processes);
          const projection = mainProfile.apply(mainProfile.prepare(input));
          try {
            Object.assign(result, await appRuntime.launchApp(codexApp.binary, primaryHome, service.config.api_key, path.join(dataHome, 'config.json'), 'main'));
          } catch (error) {
            projection.rollback();
            throw new Error('启动失败，已恢复切换前配置：' + safeError(error));
          }
          Object.assign(result, { home: primaryHome, backup: projection.backup });
        } else if (options.target === 'app') {
          Object.assign(result, await appRuntime.launchApp(codexApp.binary, result.home, service.config.api_key, path.join(dataHome, 'config.json'), 'isolated'));
        }
        if (options.channel === 'codex') prefs.account_speeds = { ...prefs.account_speeds, [options.account_id]: options.speed };
        prefs = { ...prefs, directory: options.target === 'cli' ? options.directory : prefs.directory, target: options.target, app_mode: options.app_mode || prefs.app_mode, channel: options.channel, account_id: options.account_id, model: options.model, effort: options.effort, context_window: options.context_window, compact_limit: options.compact_limit }; savePrefs();
        return result;
      } finally { switchingApp = false; }
    }
    case 'restoreMainApp': {
      if (switchingApp) throw new Error('正在切换 Codex，请等待完成');
      switchingApp = true;
      try {
        codexApp = await appRuntime.discoverApp(prefs.app_path);
        if (!codexApp.installed) throw new Error(codexApp.error);
        const processes = await appRuntime.mainProcesses(codexApp.binary);
        if (!(await confirmMainRestart(true))) return { cancelled: true };
        await appRuntime.closeMainProcesses(codexApp.binary, processes);
        const result = mainProfile.restoreManaged(dataHome, primaryHome);
        try { await appRuntime.launchApp(codexApp.binary, primaryHome, service.config.api_key, path.join(dataHome, 'config.json'), 'main'); }
        catch (error) { result.warning = '原配置已恢复，请手动打开 Codex App：' + safeError(error); }
        return result;
      } finally { switchingApp = false; }
    }
    case 'selectAccount': {
      const account_id = requireID(input);
      const result = await service.request('/api/codex/select', 'POST', { account_id });
      prefs.account_id = account_id; savePrefs(); return result;
    }
    case 'chooseApp': {
      const result = await dialog.showOpenDialog(window, { title: '选择 Codex App', properties: ['openFile'], ...(process.platform === 'linux' ? {} : {filters: [{ name: 'Codex App', extensions: process.platform === 'darwin' ? ['app'] : ['exe'] }]}) });
      if (result.canceled) return null;
      const binary = appRuntime.appExecutable(result.filePaths[0]); if (!binary) throw new Error('请选择 Codex/ChatGPT 应用，或其完整安装目录内的图形主程序');
      prefs.app_path = binary; savePrefs(); codexApp = await appRuntime.discoverApp(binary); return codexApp;
    }
    case 'workStatus': return work.snapshot();
    case 'workTabs': return work.tabs(input);
    case 'workOpen': return work.open(requireID(input));
    case 'workUnbind': return work.unbind(requireID(input));
    case 'workBind': {
      requireID(input?.account_id);
      const { accounts } = await service.request('/api/accounts');
      return work.bind(input,accounts.find(a=>a.id===input.account_id));
    }
    case 'pelicanHistory': return { batches: pelican.snapshot(), default_prompt: DEFAULT_PROMPT, candy_prompt:CANDY_PROMPT };
    case 'pelicanStart': {
      const { accounts } = await service.request('/api/accounts');
      return pelican.start(input, accounts);
    }
    case 'pelicanCancel': pelican.cancel(); return true;
    case 'pelicanDelete': pelican.remove(input); return true;
    case 'pelicanSource': return pelican.artifact(input).text;
    case 'pelicanExport': {
      const artifact = pelican.artifact(input);
      const candy = artifact.benchmark === 'candy';
      const result = await dialog.showSaveDialog(window, { title: candy ? '保存糖果题回答' : '保存鹈鹕动画 HTML', defaultPath: candy ? 'candy-answer.txt' : 'pelican.html', filters: [{ name: candy ? '文本' : 'HTML', extensions: [candy ? 'txt' : 'html'] }] });
      if (result.canceled) return false;
      await fsp.writeFile(result.filePath, candy ? artifact.text : artifact.html, { encoding: 'utf8', mode: 0o600 }); return true;
    }
    case 'test': {
      requireID(input?.account_id);
      if (typeof input.model !== 'string' || !['low', 'medium', 'high', 'xhigh', 'max'].includes(input.effort)) throw new Error('测试参数无效');
      if (input.channel !== 'work' && input.effort === 'max') throw new Error('仅 Work 通道支持 max 推理档位');
      return service.request('/api/test', 'POST', { account_id: input.account_id, model: input.model, effort: input.effort, route: cleanChannel(input.channel), prompt: 'Reply with only: CONNECTION OK' }, 150000);
    }
    case 'saveSettings': return service.request('/api/settings', 'PUT', cleanSettings(input));
    case 'preferences':
      if (input?.account_speed) {
        const { id, speed } = input.account_speed; requireID(id);
        if (!['standard', 'fast'].includes(speed)) throw new Error('速度设置无效');
        prefs.account_speeds = { ...prefs.account_speeds, [id]: speed }; savePrefs();
      }
      if (typeof input?.compact_view === 'boolean') { prefs.compact_view = input.compact_view; savePrefs(); }
      if (typeof input?.restore_on_exit === 'boolean') { prefs.restore_on_exit = input.restore_on_exit; savePrefs(); }
      if (typeof input?.prism_url === 'string') { prefs.prism_url = prismURL(input.prism_url); savePrefs(); }
      for (const key of ['channel', 'pelican_channel']) if (input && key in input) { prefs[key] = cleanChannel(input[key]); savePrefs(); }
      if (input && ['system', 'light', 'dark'].includes(input.theme)) { prefs.theme = input.theme; nativeTheme.themeSource = input.theme; savePrefs(); }
      return prefs;
    case 'logs': return service.request('/api/logs');
    case 'exportQuality': {
      const logs = await service.request('/api/logs');
      const records = (logs.records || []).filter(record => record.quality);
      if (!records.length) throw new Error('暂无可导出的质量记录');
      const result = await dialog.showSaveDialog(window, { title: '导出协议观测', defaultPath: `codex-signals-${new Date().toISOString().slice(0, 10)}.json`, filters: [{ name: 'JSON', extensions: ['json'] }] });
      if (result.canceled) return false;
      await fsp.writeFile(result.filePath, JSON.stringify({ exported_at: new Date().toISOString(), records }, null, 2), { encoding: 'utf8', mode: 0o600 });
      return true;
    }
    case 'openLink': await shell.openExternal(externalLink(input)); return true;
    case 'copyBusinessCode': clipboard.writeText(businessCode(input)); return true;
    case 'openData': {
      const error = await shell.openPath(dataHome); if (error) throw new Error(error); return true;
    }
    case 'repairService': return service.start();
    case 'copyInstall': clipboard.writeText('npm install -g @openai/codex'); return true;
    case 'about': return { version: app.getVersion(), platform: process.platform, arch: process.arch, home: dataHome, log: service.logPath };
    default: throw new Error('不支持的操作');
  }
}
if (lock) {
  app.on('second-instance', show);
  app.on('activate', show);
  app.on('window-all-closed', () => {});
  app.on('before-quit', event => { if (!allowQuit) { event.preventDefault(); void requestQuit(); } });
  app.whenReady().then(async () => {
    app.setAccessibilitySupportEnabled(true);
    setupPath();
    await fsp.mkdir(dataHome, { recursive: true, mode: 0o700 });
    readPrefs();
    codexApp = await appRuntime.discoverApp(prefs.app_path);
    nativeTheme.themeSource = prefs.theme || 'system';
    const platformFolder = {win32:'win',darwin:'mac',linux:'linux'}[process.platform];
    if (!platformFolder) throw new Error('当前系统暂不支持桌面客户端');
    const executable = process.platform === 'win32' ? 'gptbridge.exe' : 'gptbridge';
    const binary = app.isPackaged ? path.join(process.resourcesPath, 'backend', executable) : path.join(__dirname, 'backend', `${platformFolder}-${process.arch}`, executable);
    work = new Work(dataHome); await work.listen();
    service = new BridgeService({ home: dataHome, binary, work });
    autoLogin = new AutoLogin({ service, home: dataHome });
    subscriptionBinding = new SubscriptionBinding({ service, home: dataHome });
    pelican = new Pelican(dataHome, (body, signal) => service.request('/api/pelican/generate', 'POST', body, 910000, signal));
    await pelican.listen();
    const icon = nativeImage.createFromPath(path.join(__dirname, 'assets', 'icon.png'));
    window = new BrowserWindow({ width: 1400, height: 940, minWidth: 980, minHeight: 690, show: false, title: 'WishToken Desktop', backgroundColor: '#f3f6fc', icon, autoHideMenuBar: true, webPreferences: { preload: path.join(__dirname, 'preload.cjs'), contextIsolation: true, nodeIntegration: false, sandbox: true } });
    window.webContents.setWindowOpenHandler(() => ({ action: 'deny' }));
    window.webContents.on('will-navigate', event => event.preventDefault());
    window.webContents.on('will-frame-navigate', event => { if (!event.isMainFrame && !pelican.allowedPreview(event.url)) event.preventDefault(); });
    window.webContents.session.setPermissionRequestHandler((_wc, _permission, callback) => callback(false));
    window.on('close', event => { if (!quitting) { event.preventDefault(); window.hide(); } });
    tray = new Tray(icon.resize({ width: process.platform === 'darwin' ? 22 : 20, height: process.platform === 'darwin' ? 22 : 20 }));
    tray.setToolTip('WishToken Desktop · 本地 Codex 服务');
    tray.setContextMenu(Menu.buildFromTemplate([{ label: '打开 WishToken Desktop', click: show }, { type: 'separator' }, { label: '退出客户端', click: requestQuit }]));
    tray.on('double-click', show);
    if (process.platform === 'darwin') Menu.setApplicationMenu(Menu.buildFromTemplate([{ label: 'WishToken Desktop', submenu: [{ label: '显示主窗口', click: show }, { type: 'separator' }, { label: '退出', accelerator: 'Command+Q', click: requestQuit }] }, { role: 'editMenu' }, { role: 'windowMenu' }]));
    else Menu.setApplicationMenu(null);
    ipcMain.handle('gptbridge', async (event, method, data) => {
      if (!validSender(event)) return { ok: false, error: '无效的客户端来源' };
      try { return { ok: true, data: await handle(method, data) }; }
      catch (error) { return { ok: false, error: safeError(error) }; }
    });
    await service.start().catch(() => {}); // The UI presents a recoverable error.
    await window.loadURL(page);
    show();
  }).catch(error => { dialog.showErrorBox('WishToken Desktop 启动失败', safeError(error)); allowQuit = true; app.quit(); });
}
