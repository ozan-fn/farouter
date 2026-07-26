# open-sse — SSE Request-to-Response Flow

Provider-agnostic SSE engine. Accept OpenAI/Claude/Gemini-style request, route to 290+ upstream providers, translate formats, stream response back as SSE.

---

## High-Level Flow

```
Client Request
  → src/app/api/v1/.../route.ts (Next.js route, OmniRoute only)
    → handlers/chatCore.ts::handleChatCore()
      → detectFormat() — heuristic format detection from body shape
      → resolve provider/model → getTargetFormat() → getExecutor()
      → translateRequest() — convert client format → upstream format
      → executor.execute() — HTTP POST to upstream, retry loop
      → translateResponse() — convert upstream format → client format
      → SSE TransformStream — parse, normalize, format, enqueue
  → SSE/JSON response to client
```

Two entry points:
- **9router**: `index.js` exports `handleChatCore` directly (Next.js routes in 9router project)
- **OmniRoute**: `index.ts` exports same API, served via `src/app/api/v1/*/route.ts`

---

## Phase 1: Request Entry

### Format Detection — `services/provider.js/ts`

`detectFormat(body)` heuristics:

| Condition | Format |
|-----------|--------|
| `body.input` array/string + no `messages` | openai-responses |
| `body.request?.contents` + `userAgent=antigravity` | antigravity |
| `body.contents` array | gemini |
| OpenAI fields (stream_options, response_format, logprobs, n, etc.) | openai |
| `body.messages` + Claude structure (system, anthropic_version, tool_use, base64) | claude |
| Fallback | openai |

Additional check in `translator/formats.js/ts`:
- `/v1/responses` → openai-responses
- `/v1/messages` → claude
- `/v1/chat/completions` + `input[]` → openai

### Provider/Model Resolution — `services/model.js/ts`

`parseModel(modelStr)` → `{ provider, model }`. Supports `provider/model` syntax. Falls back to `inferProviderFromModelName()` regex map:
- `claude-` → anthropic, `gemini-` → gemini, `gpt-` → openai, `deepseek-` → openrouter

### Bypass Requests — `utils/bypassHandler.js`

Warmup, skip patterns, Claude CLI CC rename requests → fake immediate response, skip upstream call.

---

## Phase 2: Pre-Translation

### Modality Stripping — `translator/concerns/modality.js/ts`

Strip vision/audio/pdf blocks model cannot handle.

### Remote Image Prefetch — `translator/concerns/prefetch.js/ts`

Convert remote image URLs to base64 for providers that cannot fetch URLs.

### Native Passthrough — `services/provider.js/ts`

If CLI tool matches provider ecosystem (Claude CLI → anthropic), skip translation entirely.

---

## Phase 3: Translation

### Orchestration — `translator/index.js/ts`

`translateRequest(sourceFormat, targetFormat, model, body, stream, credentials)`:

```
1. Strip content types per model strip list
2. Normalize thinking config (remove if last message is not user)
3. Validate tool_call IDs (Anthropic pattern ^[a-zA-Z0-9_-]+$)
4. Fix missing tool responses (insert empty tool_result)
5. Capture thinking intent + session ID
6. Source ≠ Target?
   ├── Direct route (source:target) — lossless for claude:kiro etc.
   └── Pivot through OpenAI — source→OpenAI→target (hub-and-spoke)
7. Apply thinking config in target-native format
8. If target=OpenAI: filterToOpenAIFormat()
9. If target=Claude: prepareClaudeRequest()
10. If cloakToolsOnOAuth quirk: cloak tool names with _cc suffix
```

### Format Constants — `translator/formats.js/ts`

OPENAI, OPENAI_RESPONSES, CLAUDE, GEMINI, GEMINI_CLI, VERTEX, CODEX, ANTIGRAVITY, KIRO, CURSOR, OLLAMA, COMMANDCODE.

### Request Translators

| Route | File | Purpose |
|-------|------|---------|
| claude→openai | `request/claude-to-openai.js/ts` | Claude messages/system/thinking → OpenAI |
| openai→claude | `request/openai-to-claude.js/ts` | OpenAI messages → Claude format |
| gemini→openai | `request/gemini-to-openai.js/ts` | Gemini contents → OpenAI messages |
| openai→gemini | `request/openai-to-gemini.js/ts` | OpenAI → Gemini contents[] + generationConfig |
| antigravity→openai | `request/antigravity-to-openai.js/ts` | Extract from `body.request` envelope |
| openai-responses | `request/openai-responses.js/ts` | Responses input → Chat Completions |
| openai→kiro | `request/openai-to-kiro.js/ts` | Kiro GenerateAssistantRequest |
| openai→cursor | `request/openai-to-cursor.js/ts` | Cursor AgentRequest protobuf |
| openai→ollama | `request/openai-to-ollama.js/ts` | Ollama chat format |
| openai→commandcode | `request/openai-to-commandcode.js/ts` | CommandCode NDJSON |
| claude→kiro | `request/claude-to-kiro.js/ts` | Direct claude→kiro (no OpenAI hop) |

### Response Translators

| Route | File | Purpose |
|-------|------|---------|
| claude→openai | `response/claude-to-openai.js/ts` | Claude SSE events → OpenAI chunks |
| openai→claude | `response/openai-to-claude.js/ts` | OpenAI chunks → Claude SSE events |
| gemini→openai | `response/gemini-to-openai.js/ts` | Gemini candidates[] → OpenAI chunks |
| openai→antigravity | `response/openai-to-antigravity.js/ts` | Antigravity streaming format |
| openai-responses | `response/openai-responses.js/ts` | Responses SSE → Chat Completions |
| kiro→openai | `response/kiro-to-openai.js/ts` | Kiro chunks → OpenAI |
| cursor→openai | `response/cursor-to-openai.js/ts` | Cursor protobuf → OpenAI |
| ollama→openai | `response/ollama-to-openai.js/ts` | Ollama NDJSON → OpenAI chunks |
| commandcode→openai | `response/commandcode-to-openai.js/ts` | CommandCode NDJSON → OpenAI |
| kiro→claude | `response/kiro-to-claude.js/ts` | Kiro OpenAI-shaped → Claude events |

### Shared Logic Modules — `translator/concerns/`

| File | Purpose |
|------|---------|
| `thinkingUnified.js/ts` | Extract + apply thinking config across 13 formats |
| `thinking.js/ts` | Thinking level/budget mapping |
| `toolCall.js/ts` | Tool call ID validation, sanitization |
| `modality.js/ts` | Strip unsupported content types |
| `prefetch.js/ts` | Remote image URL → base64 |
| `chunk.js/ts` | Streaming delta accumulation |
| `finishReason.js/ts` | Finish reason normalization |
| `image.js/ts` | Image fetch/encode |
| `json.js/ts` | JSON schema handling |
| `message.js/ts` | Message role/content manipulation |
| `paramSupport.js/ts` | Strip unsupported params per provider |
| `reasoning.js/ts` | Reasoning content mapping |
| `usage.js/ts` | Usage data extraction/normalization |

### Thinking Unified — `translator/concerns/thinkingUnified.js/ts`

Normalize reasoning config across all formats:

| Format | Config |
|--------|--------|
| openai | `reasoning_effort` (none/low/medium/high/xhigh/max) |
| claude-adaptive | `thinking.type=adaptive` + `output_config.effort` |
| claude-budget | `thinking.type=enabled` + `budget_tokens` |
| gemini-level | `generationConfig.thinkingConfig.thinkingLevel` |
| gemini-budget | `generationConfig.thinkingConfig.thinkingBudget` |
| qwen | `enable_thinking=true` + `thinking_budget` |
| deepseek | `thinking.type=enabled` + `reasoning_effort` |
| kimi | `reasoning_effort` |
| minimax | `thinking.type=adaptive` |
| model suffix | `model(high)`, `model(8192)`, `model(none)`, `model(auto)` |

---

## Phase 4: Post-Translation Token Optimizers

| Hook | File | Purpose |
|------|------|---------|
| Tool deduplication | `utils/toolDeduper.js` | Strip built-in tools when MCP equivalents exist |
| RTK Token Saver | `rtk/index.js` | Compress tool_result content in-place |
| Headroom | `rtk/headroom.js` | External proxy compression (fail-open) |
| Caveman | `rtk/caveman.js` | Inject terse-style system prompt |
| Ponytail | `rtk/ponytail.js` | Inject lazy-senior-dev system prompt |
| PXPIPE | `rtk/pxpipe.js` | Image bulky context compression (Claude only) |

---

## Phase 5: Execution

### Executor Selection — `executors/index.js/ts`

Provider ID → executor instance:

| Executor | File | Provider |
|----------|------|----------|
| DefaultExecutor | `executors/default.js/ts` | OpenAI-compatible, Anthropic-compatible |
| KiroExecutor | `executors/kiro.js/ts` | Kiro AI (AWS EventStream binary) |
| CodexExecutor | `executors/codex.js/ts` | OpenAI Codex (Responses API) |
| CursorExecutor | `executors/cursor.js/ts` | Cursor |
| Others | `executors/*.js/ts` | Antigravity, GitHub, Vertex, Qoder, etc. |

### BaseExecutor — `executors/base.js/ts`

Abstract base. `execute()` flow:
```
1. Iterate baseUrls (fallback rotation)
2. For each URL:
   a. transformRequest() — provider-specific
   b. buildHeaders() — auth (Bearer / x-api-key / combined / split)
   c. proxyAwareFetch() — HTTP POST via patched fetch
   d. Connect timeout (FETCH_CONNECT_TIMEOUT_MS = 60s)
   e. Retry on 429/502/503/504 (configurable counts, exponential backoff)
3. Return { response, url, headers, transformedBody }
```

### DefaultExecutor — `executors/default.js/ts`

Handles most providers. Key logic:
- `buildUrl()` — `openai-compatible-*`, `anthropic-compatible-*`, Gemini `streamGenerateContent`, `{accountId}` substitution
- `buildHeaders()` — auth from registry descriptors: `combined` (single header), `split` (apiKey + oauth), header hooks (kimiHeaders, clineHeaders, claudeOverlay)
- `transformRequest()` — `json_schema→json_object` fallback, `stripUnsupportedParams()`, `injectReasoningContent()`
- `refreshCredentials()` — generic OAuth refresh

### KiroExecutor — `executors/kiro.js/ts` (1201 lines)

Binary AWS EventStream protocol:
- `transformEventStreamToSSE()` — parse binary frames → OpenAI-shaped SSE chunks
- Frame types: assistantResponseEvent, reasoningContentEvent, codeEvent, toolUseEvent, messageStopEvent, meteringEvent, metricsEvent
- CRC-32 validation on every frame
- Tool call repair: detect malformed calls, retry with repair instructions
- Heartbeat (`: kiro-validation\n\n`) during long repairs

### CodexExecutor — `executors/codex.js/ts`

OpenAI Codex Responses API:
- `transformRequest()` — inject default instructions, normalize input, convert system→developer, manage prompt_cache_key
- `execute()` — prefetch remote images, retry on SSE-level overloaded errors
- `parseError()` — extract `resetsAtMs` from Codex `usage_limit_reached`

### Proxy & Network — `utils/proxyFetch.js/ts`

Patches `globalThis.fetch`. Resolution order:
1. Vercel relay (`x-relay-target` + `x-relay-path` headers)
2. Connection proxy (per-connection)
3. Environment proxy (`HTTPS_PROXY`/`HTTP_PROXY`/`ALL_PROXY`)
4. MITM DNS bypass: resolve real IP via Google DNS (8.8.8.8), raw HTTPS socket with SNI
5. Native `fetch`

Supports `no_proxy` patterns, proxy dispatcher caching, `strictProxy` mode.

---

## Phase 6: Response Dispatch

Decision tree in `handlers/chatCore.js/ts`:

```
response = executor.execute(...)

if (stream, but provider forced non-streaming OR client wants JSON):
  → handleForcedSSEToJson()
    → convertResponsesStreamToJson() or parseSSEToOpenAIResponse()
    → JSON Response

if (non-streaming + provider also non-streaming):
  → handleNonStreamingResponse()
    → read body, translateNonStreamingResponse()
    → JSON Response

if (streaming):
  → handleStreamingResponse()
    → buildTransformStream()
    → pipeWithDisconnect()
    → SSE Response (text/event-stream)
```

### Forced SSE → JSON — `handlers/chatCore/sseToJsonHandler.js/ts`

When provider forces streaming but client wants JSON:
- Codex/Responses API: `convertResponsesStreamToJson()` — accumulate all events
- Standard Chat Completions: `parseSSEToOpenAIResponse()` — merge streaming chunks
- Returns complete JSON: `{ id, object, created, model, choices: [{ message, finish_reason }], usage }`

### Non-Streaming — `handlers/chatCore/nonStreamingHandler.js/ts`

1. Read provider response body (JSON or SSE text)
2. For SSE: accumulate chunks via `parseSSEToOpenAIResponse()`
3. Decloak tool names (reverse `_cc` suffix)
4. Extract usage, persist to DB
5. `translateNonStreamingResponse()` — format-specific conversion:
   - OpenAI→Claude: `openAICompletionToClaudeMessage()`
   - Gemini/Antigravity→OpenAI: `candidates[0].content.parts`
   - Claude→OpenAI: `content[]` blocks (text/thinking/tool_use)
   - Ollama→OpenAI: `ollamaBodyToOpenAI()`
6. Fix `finish_reason` for tool_calls, strip `reasoning_content` when content non-empty

### Streaming — `handlers/chatCore/streamingHandler.js/ts`

`buildTransformStream()`:
1. Responses API provider + translation → `createSSETransformStreamWithLogger(OPENAI_RESPONSES, targetFormat)`
2. Needs translation → `createSSETransformStreamWithLogger(targetFormat, sourceFormat)`
3. Same format → `createPassthroughStreamWithLogger()`

`handleStreamingResponse()`:
1. `onRequestSuccess()` (async fire-and-forget)
2. Validate content-type is `text/event-stream` or `application/json` (reject HTML errors)
3. Build transform stream
4. `pipeWithDisconnect(providerResponse.body, transformStream, controller)`
5. Return `new Response(transformedBody, { headers: SSE_HEADERS_CORS })`

---

## Phase 7: SSE TransformStream — Core Streaming

### Stream Architecture — `utils/stream.js/ts` (+ `utils/streamHandler.js/ts`, `utils/streamHelpers.js/ts`)

```
Upstream bytes (binary chunks)
  → TextDecoder (utf-8, stream:true)
  → buffer accumulation (split on "\n")
  → per-line processing:

  PASSTHROUGH mode (no translation needed):
    - "data:" → JSON parse
    - Fix invalid IDs, inject missing OpenAI fields (object, created)
    - Strip Azure fields (prompt_filter_results, content_filter_results)
    - Strip empty tool_calls arrays (breaks AI SDK reasoning)
    - Filter vapourware chunks via hasValuableContent()
    - Accumulate content + reasoning for token estimation
    - extractUsage() → mergeUsage() across chunks
    - Inject estimated usage if finish chunk lacks it
    - Re-emit as "data: {...}\n"

  TRANSLATE mode (format conversion needed):
    - parseSSELine() — standard SSE + NDJSON/Ollama
    - Ollama: done=true = final chunk (do not skip)
    - Other formats: { done: true } = [DONE] sentinel (skip)
    - Responses API passthrough: preserve event framing, track terminal state
    - Accumulate content/reasoning per-format
      (Claude: delta.text/delta.thinking, OpenAI: delta.content/delta.reasoning_content,
       Gemini: candidates[].content.parts)
    - extractUsage() per-format
    - translateResponse(targetFormat, sourceFormat, parsed, state) → client format
    - Inject estimated usage if finish chunk has none
    - formatSSE() → enqueue TextEncoder bytes

  flush(controller):
    - Handle remaining buffer
    - Flush pending translateResponse() state
    - Synthesize response.failed for incomplete Responses streams
    - Emit [DONE] (not for Gemini family)
    - Log usage, fire onStreamComplete callback
```

### SSE Parsing — `utils/streamHelpers.js/ts`

`parseSSELine(line, format)`:
- Ollama: raw JSON (NDJSON, no `data:` prefix)
- Standard SSE: strip `data: ` prefix, parse JSON
- `[DONE]` → `{ done: true }`

`formatSSE(data, sourceFormat)`:
- OpenAI Responses API: `event: {eventType}\ndata: {json}\n\n`
- Claude: `event: {type}\ndata: {json}\n\n`
- Default: `data: {json}\n\n`

### SSE Headers — `utils/sseConstants.js/ts`

```js
{
  "Content-Type": "text/event-stream",
  "Cache-Control": "no-cache",
  "Connection": "keep-alive",
  "Access-Control-Allow-Origin": "*"
}
```

`SSE_DONE = "data: [DONE]\n\n"`

### Disconnect-Aware Piping — `utils/streamHandler.js/ts`

`createStreamController({ onDisconnect, onError, log, provider, model })`:
- Wraps AbortController
- Tracks connected/disconnected state
- `handleDisconnect(reason)` → 500ms delayed abort
- `handleComplete()` → clear timers

`pipeWithDisconnect(providerResponse, transformStream, controller, onAbortTerminal, stallTimeoutMs)`:
1. upstreamTap TransformStream — tracks byte activity, resets stall watchdog
2. SSE TransformStream — format translation + normalization
3. createDisconnectAwareStream() — checks connection state on pull
4. Stall detection: reset timer on every upstream byte. No bytes for `STREAM_STALL_TIMEOUT_MS` (360s) → abort
5. `onAbortTerminal`: for Responses API passthrough, emit `response.failed + [DONE]` on abort

### SSE Wire Formats

**OpenAI Chat Completions:**
```
data: {"id":"...","object":"chat.completion.chunk","choices":[{"delta":{"content":"Hello"}}]}\n\n
data: {"id":"...","choices":[{"delta":{},"finish_reason":"stop"}],"usage":{...}}\n\n
data: [DONE]\n\n
```

**Claude:**
```
event: content_block_delta\n
data: {"type":"content_block_delta","delta":{"text":"Hello"}}\n\n
event: message_delta\n
data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{...}}\n\n
```

**OpenAI Responses API:**
```
event: response.output_text.delta\n
data: {"type":"response.output_text.delta","delta":"Hello"}\n\n
event: response.completed\n
data: {"type":"response.completed","response":{...}}\n\n
data: [DONE]\n\n
```

**NDJSON (Ollama/CommandCode):** Raw JSON lines without `data:` prefix.

---

## Phase 8: Error Handling

### Error Response Format — `utils/error.js/ts`

OpenAI-compatible:
```json
{
  "error": {
    "message": "Bad gateway - upstream provider error",
    "type": "server_error",
    "code": "bad_gateway"
  }
}
```

### Error Types — `config/errorConfig.js/ts`

Maps HTTP status codes → OpenAI error types + cooldown rules + exponential backoff.

### Streaming Error

```
data: {"error":{"message":"...","type":"upstream_error","code":"kiro_missing_terminal"}}\n
data: [DONE]\n\n
```

### Error Flow

1. Upstream transport error (ECONNRESET, ETIMEDOUT, EPIPE, UND_ERR_SOCKET) → graceful close, `emitTerminal()` + `controller.close()`
2. Stream stall (no bytes for 360s) → `streamController.handleError()` + abort
3. Incomplete Responses stream → synthesized `response.failed + [DONE]`
4. Non-SSE content-type (HTML error page) → `new Response(JSON.stringify({error}), { status })` instead of piping

### Token Refresh on 401/403

If upstream returns 401/403 and provider is not `noAuth`:
1. Attempt token refresh (up to 3 retries)
2. Re-execute the request with fresh credentials

---

## Provider Capabilities — `providers/capabilities.js/ts`

Determines per-model capabilities (vision, audio, reasoning, search, context window, thinking format).

Lookup chain (first match wins, merged over DEFAULT_CAPABILITIES):
1. `PROVIDER_CAPABILITIES[provider][model]`
2. `MODEL_CAPABILITIES[model]`
3. `PATTERN_CAPABILITIES` (glob patterns, specific→generic)
4. `DEFAULT_CAPABILITIES`

Thinking format drives how `applyThinking()` configures the request.

---

## Provider Registry — `providers/registry/`

Auto-generated static imports of all 290+ provider definitions. Each entry:
```js
export default {
  id: "anthropic",
  transport: { baseUrl, format, headers, forceStream, retry, quirks },
  models: [{ id, name, ... }]
}
```

`providers/index.js/ts` builds `PROVIDERS`, `PROVIDER_MODELS`, `PROVIDER_OAUTH`, `PROVIDER_MEDIA`.

---

## Request Logging — `utils/requestLogger.js/ts`

Stage-by-stage file logging when `ENABLE_REQUEST_LOGS=true`:
1. `1_req_client.json` — raw client request
2. `2_req_source.json` — detected source format
3. `3_req_openai.json` — after pivot through OpenAI
4. `4_req_target.json` — final upstream format
5. `5_res_provider.json` — raw provider response
6. `5_res_provider.txt` — streaming chunks
7. `6_res_openai.txt` — OpenAI intermediate chunks
8. `7_res_client.txt` — final client format

---

## Project Differences

| Aspect | 9router/open-sse | OmniRoute/open-sse |
|--------|-----------------|-------------------|
| Language | JavaScript (ESM) | TypeScript |
| Entry | `index.js` | `index.ts` (+ compiled `index.js`) |
| MCP Server | No | `mcp-server/` (104 tools) |
| Transformer | `transformer/` (minimal) | `transformer/responsesTransformer.ts` (full) |
| Package | internal 9router package | `@omniroute/open-sse` v3.8.49 |
| Services | minimal | 134+ service modules |
| Executors | subset | 90+ executor registrations |
| Config | JS configs | TypeScript configs + Zod validation |

---

## Key Files

| File | Purpose |
|------|---------|
| `handlers/chatCore.js/ts` | Main orchestrator |
| `executors/base.js/ts` | Abstract executor with retry |
| `executors/default.js/ts` | Generic provider executor |
| `utils/stream.js/ts` | Core SSE TransformStream |
| `utils/streamHandler.js/ts` | Disconnect-aware piping + stall detection |
| `utils/streamHelpers.js/ts` | SSE parsing/formatting |
| `utils/proxyFetch.js/ts` | Proxy-aware fetch patch |
| `translator/index.js/ts` | Translation orchestrator |
| `services/provider.js/ts` | Format detection + target resolution |
| `services/model.js/ts` | Model/alias resolution |
| `config/runtimeConfig.js/ts` | Timeouts, retry config |
| `config/errorConfig.js/ts` | Error type mapping |
| `providers/capabilities.js/ts` | Per-model capability lookup |
| `providers/registry/` | 290+ provider definitions |
