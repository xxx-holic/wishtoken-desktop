'use strict';
const fs = require('node:fs/promises');
const path = require('node:path');
const os = require('node:os');
const { spawn } = require('node:child_process');
const { setTimeout: delay } = require('node:timers/promises');

async function findBrowser() {
  const env = process.env;
  const roots = [env.PROGRAMFILES, env['PROGRAMFILES(X86)'], env.LOCALAPPDATA].filter(Boolean);
  const candidates = process.platform === 'win32'
    ? roots.flatMap(root => [path.join(root, 'Google/Chrome/Application/chrome.exe'), path.join(root, 'Microsoft/Edge/Application/msedge.exe')])
    : process.platform === 'darwin'
      ? ['/Applications/Google Chrome.app/Contents/MacOS/Google Chrome', '/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge', path.join(os.homedir(), 'Applications/Google Chrome.app/Contents/MacOS/Google Chrome')]
      : ['/usr/bin/google-chrome', '/usr/bin/google-chrome-stable', '/usr/bin/chromium', '/usr/bin/chromium-browser', '/opt/google/chrome/chrome'];
  for (const candidate of candidates) { try { await fs.access(candidate); return candidate; } catch {} }
  throw new Error('请先安装 Chrome 或 Edge，再开始自动授权；也可使用手动浏览器授权。');
}

// One owned browser per login. No connection to the user's daily browser or
// research CDP, no credentials in process arguments, no persistent profile reuse.
async function openLoginBrowser(home, signal) {
  const executable = await findBrowser();
  const parent = path.join(home, 'login-profiles');
  await fs.mkdir(parent, { recursive: true, mode: 0o700 });
  const profile = await fs.mkdtemp(path.join(parent, 'session-'));
  let child, browser, closing;
  const close = () => closing ||= (async () => {
    if (browser?.isConnected()) {
      try { const cdp = await browser.newBrowserCDPSession(); await cdp.send('Browser.close'); } catch {}
      await browser.close().catch(() => {});
    }
    if (child && child.exitCode === null) {
      await Promise.race([new Promise(resolve => child.once('exit', resolve)), delay(1500)]);
      if (child.exitCode === null) child.kill();
    }
    // Only the directory created by this invocation is eligible for removal.
    try { await fs.rm(profile, { recursive: true, force: true, maxRetries: 8, retryDelay: 250 }); }
    catch { throw new Error('授权已结束，但临时浏览器目录清理失败；请关闭该授权窗口后清理本地数据目录中的 login-profiles。'); }
  })();
  try {
    signal.throwIfAborted();
    child = spawn(executable, [`--user-data-dir=${profile}`, '--remote-debugging-address=127.0.0.1', '--remote-debugging-port=0', '--no-first-run', '--no-default-browser-check', '--disable-sync', '--disable-background-mode', '--password-store=basic', 'about:blank'], { windowsHide: true, stdio: 'ignore' });
    let failed = false;
    child.once('error', () => { failed = true; });
    const deadline = Date.now() + 20000;
    let port;
    while (Date.now() < deadline) {
      signal.throwIfAborted();
      if (failed || child.exitCode !== null) throw new Error('授权浏览器未能启动，请关闭残留授权窗口后重试。');
      try {
        const first = (await fs.readFile(path.join(profile, 'DevToolsActivePort'), 'utf8')).split('\n')[0];
        if (/^\d+$/.test(first) && Number(first) > 0 && Number(first) <= 65535) { port = first; break; }
      } catch {}
      await delay(150, undefined, { signal });
    }
    if (!port) throw new Error('授权浏览器启动超时，请检查 Chrome / Edge 是否可以正常打开。');
    browser = await require('playwright-core').chromium.connectOverCDP(`http://127.0.0.1:${port}`, { timeout: 10000 });
    signal.throwIfAborted();
    const context = browser.contexts()[0];
    const page = context.pages()[0] || await context.newPage();
    page.setDefaultTimeout(4000);
    return { page, close };
  } catch (error) { await close(); throw error; }
}
module.exports = { findBrowser, openLoginBrowser };
