# WishToken Desktop

[English](README.en.md) · [下载客户端](https://github.com/xxx-holic/wishtoken-desktop/releases) · [账号商城](https://wishtoken.team/) · [API 中转](https://wishtoapp.com/)

WishToken 的开源 Codex 桌面配套工具。导入你有权使用的 Team 子号 JSON，查看额度、切换主 Codex App 账号，继续原有项目和已保存会话。也支持独立实例和 CLI。Windows、macOS 与 Linux 使用同一套界面和本地服务。

**[TG 官方交流群](https://t.me/wishtoken_group) · [TG 通知频道](https://t.me/wishtoken_channel) · [咨询合作](https://t.me/wishtoken_team)**

**账号留在本机。通道由你选择。请求结果可以核对。**

![WishToken Desktop 账号界面](docs/assets/accounts.png)

*界面示例使用合成账号与演示额度，不包含真实用户数据。*

## 功能

- 多账号导入：支持 Sub2API 导出、Codex auth JSON 及兼容的账号 JSON；搜索、备注、排序、额度进度和重置时间。
- 子号账密 + 2FA 导入：每行 `邮箱----密码----2FA种子`，默认纯协议完成原生或 BPS 授权；多工作区明确选择，逐项显示结果，浏览器自动化仅作显式备用。
- 选择 BPS、原生 Codex 或 ChatGPT Work 通道，默认 BPS；会话固定账号与通道，失败不会静默改用另一通道。Work 为本地预览功能，需单独绑定网页登录。
- BPS 传输可显式选择 HTTP 或实验 WebSocket；运行记录区分输入、缓存、输出和推理 tokens。验证范围见 [BPS 传输与对照测试](docs/BPS_TRANSPORT.md)。
- 焚诀订阅通道：独立设备授权、ChatGPT 订阅绑定检查、Responses API、工具与图片输入；可启动隔离 Codex App / CLI，记录 BYOK 与费用字段。实测及边界见 [焚诀预览说明](docs/FENJUE_PREVIEW.md)。
- 一键切换并启动：默认切换主 Codex App，沿用原有项目、会话和应用数据；无需项目目录。首次接管备份配置，设置中可恢复；独立实例可手动选择。CLI 按账号和项目隔离。
- 原生通道可保存每个账号的标准／快速偏好。运行记录分别展示请求与上游返回的服务等级；请求快速不等于上游已分配快速。
- 测智对比：鹈鹕动画与糖果题，同提示词、模型和推理档位，支持取消、重试、历史、隔离预览及原文导出。糖果题按结论 21 判定本题结果，答案不进入请求。
- 本地管理 API 鉴权、固定回环监听、受限 IPC、外部链接白名单、模型 HTML 预览隔离。

“不降智”是用户对这类工具的常见称呼。本项目能核对所选模型、推理档位、通道、工具往返和上游报告的字段，**不能证明上游内部使用了哪个模型，也不能保证输出质量**。鹈鹕作品是直观比较，不是 IQ 分数。

## 下载和开始使用

当前预览版本为 `0.9.0-fenjue-preview.1`，各平台下载及验证状态见 [Releases](https://github.com/xxx-holic/wishtoken-desktop/releases)。新增焚诀通道已实测原始糖果题、鹈鹕动画、图片、函数工具及真实 Codex CLI 文件操作；尚无“同账号原生答错、焚诀答对”的降智恢复对照，见 [焚诀说明](docs/FENJUE_PREVIEW.md)。BPS 独立授权和子号协议导入见 [BPS 说明](docs/BPS_TRANSPORT.md)；Work 登录、API 和测试步骤见 [Work 预览说明](docs/WORK_PREVIEW.md)。

在 [Releases](https://github.com/xxx-holic/wishtoken-desktop/releases) 下载：

| 平台 | 文件 | 状态 |
| --- | --- | --- |
| Windows x64 | `WishToken-Desktop-<版本>-win-x64.exe` 或 `.zip` | 安装包 / 便携包，未签名 |
| macOS Apple Silicon | `WishToken-Desktop-<版本>-mac-arm64.zip` | macOS 13+，未签名、未公证测试包 |
| macOS Intel | `WishToken-Desktop-<版本>-mac-x64.zip` | macOS 13+，未签名、未公证测试包 |
| Linux x64 | `WishToken-Desktop-<版本>-linux-x64.AppImage`、`.deb`、`.tar.gz` | Ubuntu 24.04 原生构建；CLI 联动，GUI App 需另行安装兼容版本 |

1. 安装客户端，并另外安装官方 Codex App 或 CLI。客户端不会捆绑官方 Codex。
2. 粘贴自己的账号 JSON，或从“授权登录”按账密 + 2FA 完成原生/BPS 协议授权，再刷新额度。
3. 选择账号、通道、模型与推理档位，点击“切换并启动”。App 无需填写目录。
4. 遇到 BPS 403 时查看错误；额度正常不代表 BPS 权限正常。可手动选择原生通道测试。

Linux 安装和系统终端要求见 [Linux 说明](docs/LINUX.md)。本项目提供 WishToken 客户端，不捆绑或承诺各平台的官方 Codex 图形应用。

详细操作、升级及平台限制见 [使用说明](docs/QUICKSTART.md)。Apple Silicon/Intel 的包结构和核心交叉编译可检查，但不等于已通过所有 Mac 实机场景；以每个版本的发布说明为准。

## 码愿 AI × WishToken

**账号现货 · 订阅代充 · API 中转，一站配齐。**

- **[WishToken 账号商城](https://wishtoken.team/)**：ChatGPT、Claude、Gemini、Grok、Cursor、Muse 等账号、会员、席位与积分套餐；自营 Team5X / GPT Free，提供 ChatGPT、Claude、Grok 正价代充。
- **[多模型 API 中转](https://wishtoapp.com/)**：GPT、Claude、DeepSeek、Kimi、MiniMax，按量计费、分组自选。[查看当前渠道与倍率](https://sub2api.wishtoapp.com/guide/channels?utm_source=wishtoken)。
- **[链动小铺](https://wzyp.cn/shop/6YHGJOPN)**：更多账号、订阅与配套服务。价格、库存、交付及质保规则以所购商品页面为准。
- **批发与合作**：批量采购、免费卡密铺货、自动补池、号商入驻。[托管发车内测](https://wishtoken.team/shop/dispatch)可查看母号、排期与产出，支持 TXT / ZIP 取货；新接入联系 [@wishtoken_team](https://t.me/wishtoken_team)，以页面开放状态为准。分销码：`r5tcjnab`。

**社群已迁移**：旧 TG 群转为只读。请使用 [唯一交流群](https://t.me/wishtoken_group)、[唯一通知频道](https://t.me/wishtoken_channel)；QQ 交流群 `1065568670`，在 QQ 搜群加入。

桌面端“商城与合作”页集中提供上述入口和代码复制。业务页面在系统浏览器打开；管理本地账号无需登录商城，导入凭据不会自动发送给 WishToken。

## 从源码构建

需要 Go 1.25+、Node.js 24、npm。Go 核心没有第三方依赖，桌面采用 Electron 和原生 HTML/CSS/JS。构建协议授权组件还需要 Python 3.12：在仓库根目录运行 `python -m pip install -r desktop/protocol/requirements.txt pyinstaller==6.21.0`；Windows/macOS/Linux 安装包自带对应组件，使用者无需安装 Python。

```sh
git clone https://github.com/xxx-holic/wishtoken-desktop.git
cd wishtoken-desktop
go vet ./...
go test ./...
cd desktop
npm ci
npm run backend
npm test
# Windows
npm run backend -- win32 x64
npm start
npm run dist:win
# macOS，在 Mac 上构建两种架构
npm run dist:mac
# Linux x64，在 Linux 上构建
npm run dist:linux
```

发布构建使用 GitHub Actions，生成独立平台产物和 `SHA256SUMS.txt`。版本发布和验证步骤见 [维护指南](CONTRIBUTING.md)。

## 隐私与贡献

本地数据默认位于 `~/.gptbridge-desktop`，沿用早期版本路径以支持升级。该目录包含敏感凭据，不应提交、分享或作为问题附件。详见 [隐私说明](docs/PRIVACY.md)、[安全报告](SECURITY.md) 与 [贡献指南](CONTRIBUTING.md)。

源码采用 MIT 许可证。Electron/Chromium 和参考项目的许可证、来源说明见 [NOTICE](NOTICE.md)。本项目与 OpenAI、Microsoft 无隶属或背书关系。
