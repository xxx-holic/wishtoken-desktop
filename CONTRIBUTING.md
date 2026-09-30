# 贡献与发布

请先在 Issue 中说明用户可见的问题、系统版本和脱敏复现步骤。协议修改应附确定性的本地 mock 测试；不要用真实账号构成测试夹具，也不要把上游回复能力等同于内部模型质量。

## 提交与合并

欢迎通过 Fork 和 Pull Request 贡献改进：

1. 从最新 `main` 创建功能分支，一次 PR 解决一个明确问题。
2. 在 PR 中说明原来的问题、修改后的行为、验证结果与已知限制；界面修改附脱敏截图。
3. 提交前完成下文的本地验证。PR 必须通过 `source`、`test (ubuntu-latest)`、`test (windows-latest)`、`test (macos-latest)` 四项 GitHub Actions 检查，并与最新 `main` 同步。
4. 维护者审查实现、兼容性、凭据保护和验证结果，解决审阅讨论后，以 **Squash and merge** 合并。仓库内已合并的功能分支自动删除，贡献者的 Fork 不受影响。

`main` 禁止直接推送、强制推送和删除；保护规则同样适用于管理员。所有代码变更都经过 PR。当前由单一维护者管理，不强制第二位维护者批准自己的 PR；外部贡献仍需维护者审查并执行合并，检查通过不会自动合并。团队扩大后可增加必需审批人数。

首次外部贡献的 Actions 运行可能需要维护者批准。不要为了通过检查而移除安全检查、上传真实凭据或降低测试要求；失败时先定位原因并在 PR 中说明。

## 项目结构

- `desktop/`：Electron 主进程、受限 IPC、原生界面、账号切换和作品比较。
- `internal/`：Go 核心，账号存储、上游适配、路由、模型清单和本地服务。
- `cmd/gptbridge/`：兼容的核心服务入口；名称保留以便升级。
- `.github/workflows/`：测试及跨平台发布。
- `scripts/check-public.py`：源码和路径公开性检查。

新功能应说明数据流、兼容性和失败行为。不要添加静默的模型/档位/账号/通道替换，不要引入宽泛 shell IPC、关闭渲染器沙箱或开放远程管理监听。

## 本地验证

```sh
python scripts/check-public.py
gofmt -w ./cmd ./internal
go vet ./...
go test ./...
cd desktop
npm ci
npm run backend
npm test
npm run smoke
```

`smoke` 使用合成账号运行真实 Electron/IPC/Go 集成检查，不访问真实账号，也不关闭开发者的主 Codex App。Windows 直接运行；Linux 使用 `dbus-run-session -- xvfb-run -a npm run smoke`。截图只写入忽略的 `build/validation/`。Mac 原生联动应在 Mac 实机验收，不能用交叉编译代替。

## 版本发布

1. 同步修改 `desktop/package.json`、`desktop/package-lock.json` 的版本，更新 `CHANGELOG.md`。
2. 运行源码检查、Go vet/test 和桌面测试，核对发布范围。
3. 为验证过的 main 提交打 `v<版本>` 标签。发布 workflow 验证标签与包版本一致，分别构建 Windows x64、Mac arm64、Mac x64 和 Linux x64。Linux 三种包需通过源码一致性、ELF 和包结构校验，以及打包版 Xvfb GUI smoke。
4. 工作流生成校验和并创建 **草稿** Release。维护者检查所有产物、签名状态和验收结果后再发布。未完成 Mac 实机验收的版本注明开发测试状态；Linux Xvfb 不代表全部发行版和真实显卡验收，不声称已签名或已公证。
5. 发布页只保留该版本安装/便携包、校验和及必要说明。源码由 Git 标签管理，不上传账号、内部报告、交接或本地会话。

依赖升级由 Dependabot 提 PR；自动检查通过不等于应自动合并。邀请码由维护者不定期发布公告，代码、CI 和安装包不嵌入有效邀请码或运营密钥。
