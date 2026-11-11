# Prism 外部桥通道

WishToken 可以把 Codex 指向本机运行的 [`prism-bridge`](https://github.com/yyyllllming/prism-bridge)。本地目录包含 GPT-6.1 Sol、GPT-5.6 Sol、GPT-5.6 Terra、GPT-6 Luna；实际可用性以账号当前请求为准。这是一个**外部依赖**，WishToken 不内置 Prism 的登录与协议实现，本轮也未验证 Prism 真实生成。

## 前置条件

1. 本机安装 Python 3.10+ 和 Playwright 浏览器。
2. 能登录 `prism.openai.com` 的账号，并在 Prism 里选择要用的模型。
3. 单独运行 prism-bridge，默认监听 `127.0.0.1:18765`：

```sh
git clone https://github.com/yyyllllming/prism-bridge
cd prism-bridge
python -m pip install -r requirements.txt
python bridge.py login
python bridge.py serve
```

如果 prism-bridge 启用了 `PRISM_BRIDGE_API_KEY`，在 WishToken 的 Prism 通道里填写同一个密钥；否则留空。

## 在 WishToken 中使用

1. 服务通道选择 **Prism 外部桥（6.1 Sol）**。
2. 填写 Prism 桥地址，默认 `http://127.0.0.1:18765`。
3. 选择模型和推理档位（low / medium / high / max / ultra）。上游桥的 README 说明 xhigh 会映射为 high，因此本项目不提供该选项，以免静默改变档位。
4. 启动方式选择 Codex App 时只能使用**独立实例**；Prism 不会接管主 Codex 配置。

Prism 使用它自己的浏览器登录，不会消耗所选账号的 Codex 凭据；所选账号只用于本地启动记录的标识。

## 限制

- Prism 是网页端自动化，不是官方 API；界面或协议变化可能导致失效。
- 上游 README 的目录不代表每个账号的当前权限；本项目不根据公告或目录推断接口存活。
- Prism 有每账号速率限制；短时间连续多轮可能被拒绝。
- 登录期限以实际会话为准，过期后需重新登录。
- 仍然受 OpenAI 账号路由/风控影响；Prism 不是“必然不降智”的保证。
- 使用非公开网页接口可能违反服务条款，账号风险由使用者承担。请只在本人账号和本机使用，不要对外提供中转。
