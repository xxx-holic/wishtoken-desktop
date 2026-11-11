# 致谢与来源 / Attribution

WishToken Desktop（核心沿用 GPTBridge 名称）是一个独立实现，代码以 MIT 许可证发布。Basispoints（Excel 插件后端）协议
本身没有公开文档，本项目对协议的理解来自下列公开项目的源码阅读与其作者的验证记录，
在此致谢：

| 项目 | 许可证 | 借鉴内容 |
| --- | --- | --- |
| [hloolx/codex2api](https://github.com/hloolx/codex2api)（基于 [james-6-23/codex2api](https://github.com/james-6-23/codex2api)） | MIT | Basispoints 上游路由、`run_officejs` 传输信封、工具目录文字化、双上游回退策略、模型白名单思路 |
| [ranxi2001/sub2api](https://github.com/ranxi2001/sub2api)（基于 [Wei-Shaw/sub2api](https://github.com/Wei-Shaw/sub2api)） | LGPL-3.0 | 附件上传（`/basispoints/api/attachments`）流程、账号导出 JSON 结构 |
| [MIKUbiu/bps-local](https://github.com/MIKUbiu/bps-local) | Unlicense | 本地回环桥的安全约束、凭据只读设计、请求头集合 |
| [sunboyzoey/bps-local-public](https://github.com/sunboyzoey/bps-local-public)（7134964） | 快照标注 Unlicense，部分沿用协议注明 MIT 来源 | WebSocket 握手字段、`response.create` 和控制事件形态参考；本项目使用 Go 标准库独立实现帧处理，未引入其第三方 WebSocket 库 |
| [lingxi-lx/excel-codex-bridge](https://github.com/lingxi-lx/excel-codex-bridge) | Unlicense | 模型别名（`-1m`）、上下文/压缩阈值、稳定前缀的提示词布局、图片上传细节 |
| [Nonary/ghcp_proxy](https://github.com/Nonary/ghcp_proxy) | Unlicense | Excel 会话与 Basispoints 协议的最早公开整理 |
| [jlcodes99/cockpit-tools](https://github.com/jlcodes99/cockpit-tools) | CC BY-NC-SA 4.0 | 产品形态参考（多账号管理、一键切号、用量展示、各平台安装包）。**未复制其代码。** |
| [router-for-me/CLIProxyAPI](https://github.com/router-for-me/CLIProxyAPI) | MIT | CPA 凭据 JSON 文件格式（`type: codex`） |

OpenAI OAuth 客户端 ID（`app_EMoamEEZ73f0CkXaXp7hrann`）与回调地址是 Codex CLI 的公开常量。

Team 0.2 的 `internal/localcodex/models.json` 来自 2026-09-30 账号可访问的 Codex 模型目录响应，保留所选模型的原始指令；适配后的能力仅声明本地 BPS 通道支持的部分。参考 sub2api 的能力收窄方法，未复制其实现源码。

## 免责声明

Work 预览通道使用 [Playwright Core](https://github.com/microsoft/playwright) 1.63.0（Apache-2.0），连接用户明确选择的浏览器标签页或启动独立登录 Profile；不捆绑浏览器安装包。依赖许可证随桌面包保留在模块目录。Work 协议实现来自本项目对已授权网页的实测；产品入口线索参考 [prodex](https://github.com/youdie006/prodex)，未复制其源码。

- 本项目与 OpenAI、Microsoft 无关，未获得其背书。
- Basispoints 是面向 Excel 加载项的非公开后端，在 Excel 之外使用它可能违反 OpenAI 的服务条款，账号可能被限制。请只用自己的账号、在自己的设备上使用，不要暴露到公网、不要分享或转售。
- 上游接口随时可能变化；本项目不提供任何保证。
