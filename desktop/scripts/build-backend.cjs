'use strict';
const { spawnSync } = require('node:child_process');
const path = require('node:path');
const fs = require('node:fs');
const platform = process.argv[2] || process.platform;
const arch = process.argv[3] || process.arch;
if (!['win32', 'darwin', 'linux'].includes(platform) || !['x64', 'arm64'].includes(arch)) throw new Error('Expected win32/darwin/linux and x64/arm64');
const osName = { win32: 'win', darwin: 'mac', linux: 'linux' }[platform];
const folder = `${osName}-${arch}`;
const root = path.resolve(__dirname, '..', '..');
const version = require('../package.json').version;
const output = path.join(root, 'desktop', 'backend', folder, platform === 'win32' ? 'gptbridge.exe' : 'gptbridge');
fs.mkdirSync(path.dirname(output), { recursive: true });
const result = spawnSync(process.env.GO_BINARY || 'go', ['build', '-trimpath', '-buildvcs=false', `-ldflags=-s -w -X github.com/xxx-holic/wishtoken-desktop/internal/version.Version=${version}`, '-o', output, './cmd/gptbridge'], { cwd: root, env: { ...process.env, GOOS: platform === 'win32' ? 'windows' : platform, GOARCH: arch === 'x64' ? 'amd64' : 'arm64', CGO_ENABLED: '0' }, stdio: 'inherit', windowsHide: true });
if (result.error) throw result.error;
if (result.status) process.exit(result.status);
const built = fs.readFileSync(output).toString('latin1').toLowerCase();
for (const marker of [[107,105,108,111],[107,105,114,111]]) {
  if (built.includes(Buffer.from(marker).toString())) throw new Error('Legacy provider branding in backend payload');
}
if (platform !== 'win32') fs.chmodSync(output, 0o755);
console.log(`Built ${folder}`);
