# 更新记录

## 0.9.0-fenjue-preview.6 — 2026-11-27 (local preview)

- 焚诀功能默认锁定，使用 Ed25519 签名、安装标识绑定及到期校验的功能授权码解锁。登录、导入、订阅检查、绑定、启动和模型请求同时受控，旧别名与直接 API 调用不能绕过；已有账号保留。
- 提供本地签发工具及使用说明，客户端只内置公钥，私钥不进入仓库、安装包或渲染进程。
- BPS 手动授权支持预期邮箱核对、完整设备码复制和失败时清除旧设备码。授权后明确显示仍停用的账号，用户可选择启用并准备原糖果题，实际推理仍需明确发起。
- 设置页增加 BPS 独立授权、静置与复测说明，保留真实案例的前后顺序、原题、通道和结论边界。2026-11-27 的纯 BPS 新样本为 21 且人工核对策略通过；本轮没有原生复测，不据此承诺永久恢复或固定冷却时长。

## 0.9.0-fenjue-preview.5 — 2026-11-26 (local preview)

- 账号管理增加独立授权类型筛选、匹配数量与清除筛选、Ctrl/Cmd+K 搜索、Esc 清空搜索和重绘后键盘焦点保留；启动面板明确显示所选授权类型。
- 改善列表密度、浅色/深色可读性与窄窗口布局，保留现有启动、导入、测智和会话配置行为。
- BPS 预热显示三个阶段、逐模型完成数、后台进度和断点恢复。HTTP 200 流内限流与 HTTP 429 一样暂停并等待，成功响应才累计进度。
- 设备码登录未启用时提供明确错误和安全设置入口；工作区选择不再把错误页的 Retry 当作工作区。
- 授权、预热成功与能力改善分别验证，不把测试夹具或连接成功当作“降智恢复”证据。

## 0.9.0-fenjue-preview.4 — 2026-11-25 (local preview)

- 新增独立的“BPS → 原生（预热）”通道：核对同账号两套授权与代理，按实时模型准入轮流发送 `hi`，完成后续期原生授权，再启动 Codex App / CLI。
- 默认 40 条、可调整总数；BPS 使用 low 且省略 service_tier，正式原生请求保留用户设置。完成记录可复用，失败不切换通道，支持停止与断点续跑。
- 新增原生启动和请求门禁、429 等待、身份或代理变化失效、预热期间暂停额度探针，以及原题测智入口。文档见 `docs/BPS_NATIVE_PREVIEW.md`。
- 本地模拟上游和桌面界面测试验证流程，不代表真实账号“降智恢复”；此前测试账号的独立订阅冷却未被改动。

## 0.9.0-fenjue-preview.3 — 2026-11-24 (local preview)

- Align the subscription request profile, account-bound session/cache affinity and stateless tool/reasoning history with the supplied reference. Preserve explicit model, effort, cache key, token limit, prompts, tools and images.
- Reject unavailable server-side history before sending credentials instead of silently dropping it. Distinguish conversations that share a system prefix.
- Verify the selected subscription identity before and after binding, reuse its configured proxy by default, and prevent a cancelled verification from reporting success.
- Complete ordinary login and the separate scoped subscription consent, and report callback browser challenges explicitly. Keep credentials during the owner-specified 24-hour sharing cooldown; do not unlink, reauthorize or fall back to paid credits.
- Add deterministic multi-turn Responses, identity isolation, binding and packaged-UI regressions. Recognize Markdown-heading candy conclusions. Native returned 29 for the supplied sample; paired restoration remains pending the sharing-cooldown recheck.

## 0.9.0-fenjue-preview.2 — 2026-11-13

- 新增订阅账密协议绑定：输入单行邮箱、密码和 2FA 种子，默认通过独立协议完成 CSRF、OAuth、回调 state、身份和会话校验。
- 协议遇到防护页或多工作区无法自动选择时，才可切换隔离浏览器备用；本次代理仅从本机设置或输入框读取，不随仓库或安装包内置。
- 授权凭据只在本次任务内存留，完成、取消或失败后清空密码和 2FA；新增协议回调、目的地址和错误脱敏测试。
- 版本包由 GitHub Actions 生成 Windows、macOS Apple Silicon/Intel 和 Linux x64 安装包，并附 SHA256 校验文件。

## 0.9.0-fenjue-preview.1 — 2026-11-12

- 修复 Linux 发布校验：Debian 包按打包器规则将预览版本中的连字符转换为波浪号，版本和架构仍须精确匹配。
- 补充稳定版、预览版、错误版本和错误架构的回归检查；保留全部源码一致性与打包 GUI 验证。
- 产品统一使用“焚诀通道”名称，旧账号导入、通道参数和独立实例目录保持兼容；研究来源仅保留在私有维护记录中。

- 新增独立焚诀设备授权、JSON 凭据导入、ChatGPT 订阅绑定检查，以及本地 Responses API 通道。
- 焚诀凭据与原生/BPS 隔离；模型、账号、推理档位明确选择，支持工具和图片，失败不切通道或重放。
- 桌面启动、糖果题与鹈鹕测智加入焚诀；Codex App 使用独立实例，运行记录保留 BYOK、费用和返回模型。
- 修复糖果题把按形状的证明数量误识别为总数结论的问题，保留真正存在多个结论时的人工复核提示。
- 实测原题回答 21、图片识别、鹈鹕动画、函数读写往返，以及真实 Codex CLI 读取文件并用 apply_patch 写入结果。本轮 Pro 样本未做原生配对，尚无“原生失败、焚诀成功”的降智恢复对照。
- 新增“商城与合作”：账号商城、API 中转与实时倍率、链动店铺、托管发车内测、合作联系，支持复制分销码和 QQ 群号；更新 TG 交流群与通知频道，清理过时赠码宣传。
- 接入范围、测试事实和复现方式见 `docs/FENJUE_PREVIEW.md`。

## 0.9.0-bps-preview.5 — 2026-11-12

- 新增子号账密与 2FA 种子批量导入，默认走独立协议会话，支持 Codex 原生与 BPS 两种授权；浏览器自动化为显式备用选项。
- BPS 协议完成设备批准后等待后端轮询、PKCE 兑换、身份校验及入库，避免把登录页面或批准成功误当成凭据可用。
- 多工作区由用户明确选择；逐账号显示进度、失败原因，支持跳过、取消、部分成功及选中已导入账号。输入默认隐藏，授权开始后清空输入框，不保存密码或 2FA 种子。
- 增加期望邮箱与授权 client 校验，防止混用其他授权会话。错误与管理响应不回显密码、种子或令牌。
- 桌面包新增按平台架构构建的独立协议 HTTP 组件；终端用户无需安装 Python。开发构建需 Python 3.12、固定 curl_cffi 和 PyInstaller。
- 两个子号已实测原生及 BPS 纯协议授权入库；原生/BPS 糖果题均给出 21，尚不构成恢复下降能力的证据。

## 0.9.0-bps-preview.4 — 2026-11-12

- 新增账号页授权登录，可选择独立 BPS 设备授权或 Codex 原生授权；显示设备码、等待结果并支持取消。
- 同成员、同工作区的 BPS 与 Codex 凭据分别保存，导入识别 OAuth client，账号与测智列表显示授权类型；原生通道拒绝误用 BPS 授权。
- BPS 使用对应 client 刷新，验证返回身份，刷新失败后停止重放并要求重新授权；错误信息不回显上游凭据。
- 糖果题识别 LaTeX 包裹的 21；回答出现多个条件下的数字结论时保留原文并提示复核。
- 直连 BPS WebSocket 已实测生成、图片、工具读写及鹈鹕动画；9 个账号的原生/BPS 糖果题均答对，尚无“原生错、BPS 对”的差异样本。详见 `docs/BPS_TRANSPORT.md`。
- Windows 预览包通过授权、JSON 导入、Work/糖果题和原有功能四组 Electron 检查；不据此声称 Mac 实机或长期批量可靠性已验证。

## 0.9.0-bps-preview.3 — 2026-11-12

- 新增显式 BPS WebSocket 实验传输，兼容原有客户端 SSE、图片上传和工具桥接；保留 HTTP 默认值，不自动切换传输。
- 增加握手校验、帧容量限制、分片与 ping/pong、空闲超时、取消、代理和终止事件测试；Go 核心仍无第三方依赖。
- 修复 BPS 首个事件即失败时仍返回 HTTP 200 的问题；本地流式和非流式 API 均返回 502，并记录生成失败，不重放请求。
- 请求记录分别展示输入、缓存、输出、推理 tokens；模型标识差异只作为协议信号，不直接判定能力。
- 整理此前本地预览的 Work 通道、糖果题、JSON 粘贴导入、图像能力声明、zstd 配置兼容和可选 Prism 外部桥。
- Work 超过四张图像或含不支持的工具图像时明确报错，不再静默丢弃图片。
- 直接 BPS WebSocket 生成仍待有效授权实测；外部中转的测试结果不替代直连验证。详见 `docs/BPS_TRANSPORT.md`。

## 0.8.3 — 2026-10-01

- 修复 Codex App 拖入图片时 `detail: original` 被本地 BPS 校验拒绝，原样保留图片清晰度设置。
- 移除会话历史累计 20 张图片就失败的限制；保留全部图片及顺序，并统一对消息图片和工具截图执行容量检查。
- 同一请求中的重复图片只解码一次，附件继续按账号和会话缓存；先校验全部图片再上传，最多 4 路并行上传，失败或取消后不发送不完整图片历史。
- 图片容量错误与格式错误分别提示。桥接容量为整段请求最多 1500 张、内嵌图片解码累计 32 MiB、单张 20 MiB；上游仍可能有独立限制。

## 0.8.1 — 2026-09-30

Linux 构建候选版；完整发布为 0.8.2。

- 新增 Linux x64 AppImage、deb 和 tar.gz，加入 Ubuntu 原生构建、制品校验及图形界面自动测试。
- Linux 后端路径、平台显示、兼容 App 检测和系统终端启动；继续保留 Windows / Mac 与主 App 切换功能。
- Linux 终端启动兼容独立 Codex 安装的 no-daemon 模式，支持按账号和项目继续会话。

## 0.8.2 — 2026-09-30

- 统一 Linux 各种安装包中的 x64 架构命名，修复发布校验路径。

## 0.8.0 — 2026-09-30

- 默认切换主 Codex App，保留原项目、已保存会话和应用数据目录；独立实例改为显式选项。
- 保留原供应商标识，兼容内置 OpenAI 与自定义供应商，不修改会话数据库。
- 首次接管备份、恢复原配置、外部修改检测、启动失败回滚；运行中的主 App 重启前提醒保存任务。
- 官方群入口与首批 10 个邀请码公告；凭据和私人测试数据不进入仓库或安装包。

## 0.7.0 — 2026-09-30

首个 WishToken Desktop 公共预览版本，延续早期本地客户端的数据兼容性。

- WishToken 账号卡片界面，官网、公告、GitHub 和版本下载入口。
- 多账号导入、真实额度与重置时间、搜索排序、一键切换并启动。
- BPS / 原生明确选择，默认 BPS，账号和通道固定到独立会话。
- Codex App 身份和额度适配；原生每账号标准／快速偏好。
- 运行记录区分请求与实际返回的模型、服务等级和流结束状态。
- 鹈鹕批量作品比较、历史、取消重试及隔离预览。
- 修正长流终止事件的统计，避免把只有 HTTP 200 的不完整回复当作完成。
- Windows、Apple Silicon 和 Intel 下载；发布检查、隐私政策与持续集成。

平台验收和签名状态以对应 Release 说明为准。
