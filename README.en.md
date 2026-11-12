# WishToken Desktop

[中文](README.md) · [Downloads](https://github.com/xxx-holic/wishtoken-desktop/releases) · [WishToken](https://wishtoken.team/?utm_source=github&utm_medium=readme)

An open-source desktop companion for WishToken and Codex on Windows, macOS and Linux. Import authorized Team account JSON, inspect quotas, switch the main Codex App account and continue existing projects and saved conversations. Separate App instances and isolated CLI workspaces remain available. Main App switching backs up the original configuration and offers restoration in Settings.

**[Official Telegram community](https://t.me/shouhouqun) · [10 invitation codes released September 30, 2026](https://github.com/xxx-holic/wishtoken-desktop/discussions/6)**

Select BPS or native Codex explicitly (BPS by default). Sessions remain pinned to their account and channel. Native speed preferences are recorded separately from the service tier actually reported upstream. Visual pelican comparisons support batch runs, isolated HTML previews and saved results; they are not intelligence scores or proof of the upstream model identity.

Account credentials stay in the local data directory. There is no telemetry, embedded account, invitation credential, or automatic upload to WishToken. Optional site and release links open in your browser. Invitation announcements are published irregularly through the [project announcements](https://github.com/xxx-holic/wishtoken-desktop/discussions/categories/announcements) and the website.

Preview.5 adds protocol-first child account import: paste one `email----password----TOTP seed` per line and explicitly choose Codex or BPS authorization. Accounts use separate HTTP sessions, multiple workspaces require a selection, and saved records can be selected immediately. Passwords and seeds are transient and never written to the account store. Browser automation remains an explicit fallback; JSON import is unchanged. See [authorization and verification](docs/BPS_TRANSPORT.md).

See the [setup guide](docs/QUICKSTART.md), [privacy documentation](docs/PRIVACY.md), [security policy](SECURITY.md) and [contribution guide](CONTRIBUTING.md). Release notes describe validation status. macOS downloads are unsigned development bundles unless a release explicitly states otherwise.

Linux x64 builds include AppImage, deb and tar.gz packages. See the [Linux guide](docs/LINUX.md) for desktop and terminal requirements. Codex CLI or a compatible graphical Codex installation must be installed separately.

Build with Go 1.25+, Node.js 24 and npm: `go test ./...`, then `cd desktop`, `npm ci`, `npm test` and `npm run dist:win` or `npm run dist:mac` on the appropriate platform.

The bundled protocol transport is built with Python 3.12: from the repository root, install `python -m pip install -r desktop/protocol/requirements.txt pyinstaller==6.21.0`. Build it natively for each OS/architecture using `npm --prefix desktop run login-helper`; end users do not need Python. Mac cross-packaging requires the corresponding native helper first.

MIT licensed; third-party notices are in [NOTICE.md](NOTICE.md). This project is not affiliated with OpenAI or Microsoft.
