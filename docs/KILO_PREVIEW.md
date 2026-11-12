# Kilo subscription preview

Kilo is a separate HTTP Responses channel. This integration was investigated using the user-supplied `kilo2api` source archive and a pinned snapshot of Kilo's cloud source. It is separate from Kiro, BPS and ChatGPT Work.

## Start from the desktop

1. Open **Settings → Kilo subscription** and authorize Kilo. The backend retains the independent device grant; renderer responses contain only the device code and account metadata.
2. Open the ChatGPT subscription binding page and connect the intended ChatGPT account. Kilo login and subscription binding are separate operations. Check the linked email in the desktop before testing.
3. Select the Kilo account and **Kilo subscription** channel. Use standard speed and the requested reasoning effort. Codex App uses an isolated profile; CLI uses a dedicated provider profile. Existing native/BPS credentials cannot authenticate this channel.
4. In the benchmark page, select Kilo and the original candy or pelican prompt. Results retain the original response. The candy expectation is 21 when shapes can be selected by touch; proof subcounts are not treated as alternative total answers.

Kilo credentials can also be pasted into the existing JSON import dialog:

```json
[{"type":"kilo","name":"My Kilo account","api_key":"your-kilo-api-key-here"}]
```

Native Codex `auth.json` exports are rejected for Kilo credentials. Reauthorize Kilo when its token expires; the application does not submit it to OpenAI's refresh endpoint.

## Local API

Use the existing authenticated loopback API, explicitly pinning both account and channel:

```sh
curl http://127.0.0.1:8792/v1/responses \
  -H 'Authorization: Bearer YOUR_LOCAL_BRIDGE_KEY' \
  -H 'Content-Type: application/json' \
  -H 'X-GPTBridge-Account: acc-YOUR_KILO_ACCOUNT' \
  -H 'X-GPTBridge-Channel: kilo' \
  -d '{"model":"gpt-6-astra","reasoning":{"effort":"medium"},"input":"hi","stream":false,"store":false}'
```

Both streaming and non-streaming Responses use an upstream SSE request. Function calls/results and image input follow Responses semantics. Existing Chat Completions conversion also reaches this explicit route. The bridge preserves caller instructions, tool definitions, reasoning and input; it does not inject a quality-restoration prompt. The model name is namespaced as `openai/<requested model>` upstream. Returned model identifiers are preserved.

Before generation the bridge checks the personal ChatGPT subscription state and the requested model's authenticated BYOK availability. Its candidate IDs match the supplied reference. The desktop currently exposes the three candidates for which it already has compatible Codex metadata: Astra, 5.6 Sol and 5.6 Luna. Other reference candidates can be requested through the API, but availability is checked before sending and they are not claimed live-tested.

`store:true`, background mode, conversation storage, unsupported effort and nonstandard service tiers fail explicitly. The bridge does not change accounts, fall back to Auto Free/BPS/native, or replay a failed generation. The generated Kilo Codex profile also disables client retries. A historical subscription `usageLimit` notice does not block recovery: the actual generation response determines current availability.

The upstream subscription/catalog preflight is not an atomic guarantee of Kilo's internal billing route. Request records preserve `kilo_byok`, `kilo_cost`, requested model, returned model and token counts. Unknown fields remain unknown. No claim is made that a local preflight can enforce an undocumented remote billing policy.

## Observed verification — November 12, 2026

| Check | Observed result |
| --- | --- |
| Local API `hi`, Astra/medium | Completed; 7 input tokens; `is_byok=true`, `cost=0` |
| Original candy through the local API | 21; 12 stars and 9 circles; original answer retained |
| Function tool round trip | Read a random nonce, wrote it with sum 111, final answer matched the file |
| PNG input | Read the fixture's text and number correctly |
| Original pelican | Recognizable pelican riding a bicycle; changed frames, 4 running animations and 9 SMIL elements; zero page errors |
| Actual installed Codex CLI | Read a fixture with a tool, used `apply_patch`, wrote the expected nonce and sum; exit 0 |
| Desktop UI | Synthetic Electron checks cover selection, isolated profile, device cancellation, binding status and original candy |

These results establish a usable observed Kilo route. A paired native comparison was not run for this Pro sample, so restoration of a degraded account is unproven. A decisive comparison still requires the same account, model, reasoning effort and original prompt to fail on native and pass through Kilo. Neither `is_byok`, a returned model name nor a single benchmark proves internal model weights or future reliability.

## Provenance and rerun

- Supplied archive SHA-256: `a5685b6e96535d4a4484bece7dc6eb701721e839859784887a5aa009e8142373`.
- Reviewed Kilo cloud commit: `8a3465bb27f36155700cf8eb5bec2ad3cdc73cfe`. Its subscription provider uses delegated authentication and traceability metadata. That source snapshot is distinguished from live gateway observations.
- Go implementation is independent and uses only the standard library. No archive service, proxy pool or source credential file is bundled.
- Local deterministic checks: `go test ./... -count=1` and `npm --prefix desktop test`.
- UI check: `node desktop/test/kilo-ui-smoke.cjs`. Set `GPTBRIDGE_TEST_APP` to the packaged executable to verify the built application with synthetic data.
- Live probes require the user's own current grant. Raw credentials and diagnostic captures are excluded from Git.
