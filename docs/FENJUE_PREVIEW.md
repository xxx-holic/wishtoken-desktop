# Fenjue subscription preview

Fenjue (焚诀通道) is the product name for a separate HTTP Responses integration. It has its own authorization and remains separate from BPS and ChatGPT Work. Upstream research references are retained only in maintainer records and are not bundled as personal resources.

## Start from the desktop

1. Open **Settings → Fenjue subscription** and authorize Fenjue. The backend retains the independent device grant; renderer responses contain only the device code and account metadata.
2. Open the subscription binding page and connect the intended ChatGPT account. Enter one `email----password----2FA seed` line; protocol authorization is the default, and an isolated browser is only the explicit fallback. Fenjue login and subscription binding are separate operations. The selected account's actual subscription is checked before authorization and again after it. An existing matching binding needs no new login; a different binding is not replaced. A web login by itself is not reported as a successful subscription binding. The selected account's proxy is used by default, with the global proxy as fallback; an explicit run proxy applies only to that authorization session.
3. Select the Fenjue account and **Fenjue subscription** channel. Use standard speed and the requested reasoning effort. Codex App uses an isolated profile; CLI uses a dedicated provider profile. Existing native/BPS credentials cannot authenticate this channel.
4. In the benchmark page, select Fenjue and the original candy or pelican prompt. Results retain the original response. The candy expectation is 21 when shapes can be selected by touch; proof subcounts are not treated as alternative total answers.

Fenjue credentials can also be pasted into the existing JSON import dialog:

```json
[{"type":"fenjue","name":"My Fenjue account","api_key":"your-fenjue-api-key-here"}]
```

Native Codex `auth.json` exports are rejected for Fenjue credentials. Reauthorize Fenjue when its token expires; the application does not submit it to OpenAI's refresh endpoint.

## Local API

Use the existing authenticated loopback API, explicitly pinning both account and channel:

```sh
curl http://127.0.0.1:8792/v1/responses \
  -H 'Authorization: Bearer YOUR_LOCAL_BRIDGE_KEY' \
  -H 'Content-Type: application/json' \
  -H 'X-GPTBridge-Account: acc-YOUR_FENJUE_ACCOUNT' \
  -H 'X-GPTBridge-Channel: fenjue' \
  -d '{"model":"gpt-6-astra","reasoning":{"effort":"medium"},"input":"hi","stream":false,"store":false}'
```

Both streaming and non-streaming Responses use an upstream SSE request. Function calls/results and image input follow Responses semantics. Existing Chat Completions conversion also reaches this explicit route. The bridge preserves caller instructions, tool definitions, reasoning and input; it does not inject a quality-restoration prompt. The model name is namespaced as `openai/<requested model>` upstream. Returned model identifiers are preserved.

### Local preview.3 transport update

The outgoing request profile follows the supplied 0.20.0 reference. A stable account-bound session is shared by the affinity headers and the default `prompt_cache_key`. Explicit cache keys are preserved. Session selection prefers headers, then the cache key, then thread/session metadata, then the first user turn together with the instructions, tools and leading system/developer messages. Appending a tool result keeps the same conversation identity; a shared system prefix alone does not merge different first-user turns. Requests without an anchor get a fresh identity.

Stateless input history retains encrypted reasoning, tool call IDs, outputs and image content. Local metadata, server-side item IDs, synthetic markers and the unsupported streaming usage option are removed. An empty function-argument string becomes `{}`. `max_output_tokens` defaults to 32,000 only when omitted; explicit limits and reasoning effort are preserved. Nonempty `previous_response_id` and `item_reference` fail with `full_history_required` before any upstream request: callers must send full history. No history retrieval or silent history deletion is implied.

Binding now distinguishes ordinary sign-in from the separate subscription grant. The protocol checks the signed-in identity, requests the subscription scopes, selects only the matching existing session, submits the current consent form and verifies subscription state. A login challenge is reported as requiring the explicit browser fallback. Callback success alone does not establish usability.

The November 24 test used an existing native credential export. The unchanged original candy prompt at Astra/medium returned 29 on native. Browser sign-in, subscription consent and an independent device grant completed, but both browser and device-credential status checks returned a temporary plan-sharing restriction. The owner supplied a 24-hour cooldown procedure, with some cases requiring 48 hours: stop native requests and probes, retain the grant, avoid unlinking/re-authorizing, and recheck after that interval. This is an operational recheck policy, not an upstream guarantee that eligibility returns at exactly 24 hours. No Fenjue generation or paired quality improvement has been established for this sample.

The UI reports this condition as `subscription_cooldown`; generation stops at subscription preflight without falling back to paid credits or another account. Credentials remain intact. The candy evaluator also recognizes Markdown-heading conclusions while keeping the original answer available for review.

Before generation the bridge checks the personal ChatGPT subscription state and the requested model's authenticated BYOK availability. Its candidate IDs match the supplied reference. The desktop currently exposes the three candidates for which it already has compatible Codex metadata: Astra, 5.6 Sol and 5.6 Luna. Other reference candidates can be requested through the API, but availability is checked before sending and they are not claimed live-tested.

`store:true`, background mode, conversation storage, unsupported effort and nonstandard service tiers fail explicitly. The bridge does not change accounts, fall back to Auto Free/BPS/native, or replay a failed generation. The generated Fenjue Codex profile also disables client retries. A historical subscription `usageLimit` notice does not block recovery: the actual generation response determines current availability.

The upstream subscription/catalog preflight is not an atomic guarantee of Fenjue's internal billing route. Request records preserve `fenjue_byok`, `fenjue_cost`, requested model, returned model and token counts. Unknown fields remain unknown. No claim is made that a local preflight can enforce an undocumented remote billing policy.

## Observed verification — November 12, 2026

| Check | Observed result |
| --- | --- |
| Local API `hi`, Astra/medium | Completed; 7 input tokens; `is_byok=true`, `cost=0` |
| Original candy through the local API | 21; 12 stars and 9 circles; original answer retained |
| Function tool round trip | Read a random nonce, wrote it with sum 111, final answer matched the file |
| PNG input | Read the fixture's text and number correctly |
| Original pelican | Recognizable pelican riding a bicycle; changed frames, 4 running animations and 9 SMIL elements; zero page errors |
| Actual installed Codex CLI | Read a fixture with a tool, used `apply_patch`, wrote the expected nonce and sum; exit 0 |
| Desktop UI | Synthetic Electron checks cover selection, isolated profile, device cancellation, protocol-first binding status and original candy |

These results establish a usable observed Fenjue route. A paired native comparison was not run for this Pro sample, so restoration of a degraded account is unproven. A decisive comparison still requires the same account, model, reasoning effort and original prompt to fail on native and pass through Fenjue. Neither `is_byok`, a returned model name nor a single benchmark proves internal model weights or future reliability.

## Provenance and rerun

- Reference archives, source snapshots and hashes remain in private maintainer records, distinct from live gateway observations.
- Go implementation is independent and uses only the standard library. No archive service, proxy pool or source credential file is bundled.
- Local deterministic checks: `go test ./... -count=1` and `npm --prefix desktop test`.
- UI check: `node desktop/test/fenjue-ui-smoke.cjs`. Set `GPTBRIDGE_TEST_APP` to the packaged executable to verify the built application with synthetic data.
- Live probes require the user's own current grant. Raw credentials and diagnostic captures are excluded from Git.
