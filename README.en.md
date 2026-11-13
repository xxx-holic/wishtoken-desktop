# WishToken Desktop

[中文](README.md) · [Downloads](https://github.com/xxx-holic/wishtoken-desktop/releases) · [Account shop](https://wishtoken.team/) · [API relay](https://wishtoapp.com/)

An open-source desktop companion for WishToken and Codex on Windows, macOS and Linux. Import authorized Team account JSON, inspect quotas, switch the main Codex App account and continue existing projects and saved conversations. Separate App instances and isolated CLI workspaces remain available. Main App switching backs up the original configuration and offers restoration in Settings.

**[Official Telegram community](https://t.me/wishtoken_group) · [Announcements](https://t.me/wishtoken_channel) · [Contact](https://t.me/wishtoken_team)**

Select BPS or native Codex explicitly (BPS by default). Sessions remain pinned to their account and channel. Native speed preferences are recorded separately from the service tier actually reported upstream. Visual pelican comparisons support batch runs, isolated HTML previews and saved results; they are not intelligence scores or proof of the upstream model identity.

Account credentials stay in the local data directory. There is no telemetry, embedded account, invitation credential, or automatic upload to WishToken. Optional business and release links open in your browser.

Current preview: `0.9.0-fenjue-preview.2`. See [Releases](https://github.com/xxx-holic/wishtoken-desktop/releases) for platform downloads and validation status. Fenjue adds separate device authorization, linked ChatGPT subscription checks and an explicit Responses route with tools/images. Original candy (21), animated pelican, PNG reading and actual Codex CLI file operations were observed. A degraded-native / passing-Fenjue comparison is still absent. See [Fenjue setup and verification](docs/FENJUE_PREVIEW.md).

Preview.5 adds protocol-first child account import: paste one `email----password----TOTP seed` per line and explicitly choose Codex or BPS authorization. Accounts use separate HTTP sessions, multiple workspaces require a selection, and saved records can be selected immediately. Passwords and seeds are transient and never written to the account store. Browser automation remains an explicit fallback; JSON import is unchanged. See [authorization and verification](docs/BPS_TRANSPORT.md).

See the [setup guide](docs/QUICKSTART.md), [privacy documentation](docs/PRIVACY.md), [security policy](SECURITY.md) and [contribution guide](CONTRIBUTING.md). Release notes describe validation status. macOS downloads are unsigned development bundles unless a release explicitly states otherwise.

Linux x64 builds include AppImage, deb and tar.gz packages. See the [Linux guide](docs/LINUX.md) for desktop and terminal requirements. Codex CLI or a compatible graphical Codex installation must be installed separately.

Build with Go 1.25+, Node.js 24 and npm: `go test ./...`, then `cd desktop`, `npm ci`, `npm test` and `npm run dist:win` or `npm run dist:mac` on the appropriate platform.

The bundled protocol transport is built with Python 3.12: from the repository root, install `python -m pip install -r desktop/protocol/requirements.txt pyinstaller==6.21.0`. Build it natively for each OS/architecture using `npm --prefix desktop run login-helper`; end users do not need Python. Mac cross-packaging requires the corresponding native helper first.

## WishToken services and community

Accounts, subscription top-ups and metered multi-model API access:

- [Account shop](https://wishtoken.team/): ChatGPT, Claude, Gemini, Grok, Cursor and Muse accounts, seats, subscriptions and credit packages; self-operated Team5X / GPT Free and full-price ChatGPT / Claude / Grok top-ups.
- [API relay](https://wishtoapp.com/): choose a channel and pay by usage. [Current channels and rates](https://sub2api.wishtoapp.com/guide/channels?utm_source=wishtoken).
- [Liandong shop](https://wzyp.cn/shop/6YHGJOPN): additional accounts and services. Check each product page for price, stock, delivery and warranty.
- Wholesale, reseller onboarding, free distribution keys and automatic pool replenishment. [Hosted dispatch beta](https://wishtoken.team/shop/dispatch) tracks owner accounts, schedules and output, with TXT / ZIP delivery. New onboarding is subject to page availability; contact [@wishtoken_team](https://t.me/wishtoken_team). Distribution code: `r5tcjnab`.

The old Telegram group is read-only. Use the [new community](https://t.me/wishtoken_group), [notification channel](https://t.me/wishtoken_channel), or search QQ group `1065568670`. The desktop's shop/cooperation page includes these links and copyable codes; shopping is optional and separate from local account management.

MIT licensed; third-party notices are in [NOTICE.md](NOTICE.md). This project is not affiliated with OpenAI or Microsoft.
