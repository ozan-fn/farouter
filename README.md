# farouter

**High-performance AI router for Kiro (AWS CodeWhisperer) with intelligent account rotation, integrity validation, and built-in web dashboard.**

farouter is a production-ready Go-based proxy that manages multiple Kiro accounts with smart rotation, automatic token refresh, quota tracking, and response integrity validation. Built for reliability and performance with zero external dependencies.

---

## ✨ Features

### 🔄 **Intelligent Account Management**
- **3-slot rotation** with standby queue for unlimited accounts
- **Sticky routing** (3 requests per account before rotation)
- **Automatic failover** when accounts are exhausted or suspended
- **Smart reactivation** based on quota reset times
- **Token auto-refresh** with background keepalive (every 6 hours)
- **Multi-auth support**: OAuth (Builder ID, Google, GitHub), API keys, External IdP (Microsoft Entra), IAM Identity Center (AWS SSO)

### 🛡️ **Response Integrity Validation**
- **Automatic repair** for incomplete responses (ellipsis, short finals, invalid tool calls)
- **Heartbeat keepalive** during repair (10s interval) to prevent client timeouts
- **Retry with repair instructions** injected into system prompt
- **Smart detection** of orphaned tool results across entire history
- **Short future action detection** with multi-language support (English, Chinese)

### 🌍 **Region-Aware Routing**
- **Automatic regionalization** of CodeWhisperer endpoints based on token region
- **Profile ARN discovery** with multi-region probing (us-east-1, eu-central-1)
- **Endpoint failover** (amazonaws.com → kiro.dev)
- **Auth-aware ordering** (api_key/external_idp/idc hit CodeWhisperer surface first)

### 🔧 **Advanced Request Handling**
- **RTK tool processing** (optional, configurable via UI)
- **Message conversion** with tool call reconciliation
- **Image support** (OpenAI `image_url` and Claude `image` formats)
- **Session replay** for conversation continuity
- **Thinking mode** with adaptive effort levels (minimal/low/medium/high/xhigh/max)
- **Agentic mode** with chunked write protocol for file operations
- **VansRouter-aligned specifications** for thinking budgets and effort mapping

### 📊 **Quota & Usage Tracking**
- **Real-time quota monitoring** from Kiro metering events
- **Credit vs agentic usage** detection and tracking
- **Context usage percentage** tracking
- **Automatic exhaustion detection** (402 Payment Required)
- **Suspension detection** (403 TEMPORARILY_SUSPENDED)

### 🎨 **Built-in Web Dashboard**
- **Modern React UI** with Tailwind v4 + React Router v8
- **Real-time account status** (active, exhausted, suspended, remaining tokens)
- **Account management** (add, remove, refresh tokens manually)
- **Active batch visualization** with sticky count display
- **Standby queue** management
- **Session-based authentication** (password configurable in `config.json`)
- **Brotli compression** (level 11) for optimized asset delivery

### ⚡ **Performance Optimizations**
- **SSE streaming** with client disconnect detection
- **Heartbeat keepalive** (15s global + 10s during repair)
- **Connection reuse** with configurable HTTP client
- **Zero external dependencies** (pure Go with embedded web assets)
- **Optimized binary size** (~8MB with embedded React dashboard)

---

## 🚀 Quick Start

### Prerequisites
- Go 1.21+ (for building from source)
- OR download pre-built binary from releases

### Installation

#### Option 1: Build from Source
```bash
cd farouter
go build -o farouter main.go
```

#### Option 2: Use Pre-built Binary
```bash
# Download latest release
wget https://github.com/your-repo/farouter/releases/latest/download/farouter
chmod +x farouter
```

### Configuration

1. **Create `config.json`** (or copy from `config.example.json`):
```json
{
  "password": "your-secure-password-here",
  "rtkEnabled": false,
  "accounts": [
    {
      "label": "Account 1",
      "refreshToken": "your-kiro-refresh-token-here",
      "authMethod": "builderid",
      "region": "us-east-1"
    }
  ]
}
```

2. **Obtain Kiro Refresh Tokens**:
   - Use Kiro IDE extension and extract from browser DevTools
   - Or use OAuth flow to authenticate

3. **Start farouter**:
```bash
./farouter
```

Server will start on:
- **API**: `http://localhost:20180`
- **Web Dashboard**: `http://localhost:20180` (login with password from config)

---

## 📖 Usage

### OpenAI-Compatible API

farouter exposes OpenAI-compatible chat completions endpoint:

```bash
curl -X POST http://localhost:20180/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "kr/claude-sonnet-4.5",
    "messages": [
      {"role": "user", "content": "Hello!"}
    ],
    "stream": true
  }'
```

### Supported Models

Use `kr/` prefix for Kiro models:

| Model | Description |
|-------|-------------|
| `kr/auto` | Auto-select best model (default: claude-sonnet-4.5) |
| `kr/claude-sonnet-4.5` | Claude Sonnet 4.5 |
| `kr/claude-sonnet-4.5-thinking` | With extended thinking |
| `kr/claude-sonnet-4.5-agentic` | With agentic capabilities |
| `kr/claude-sonnet-4.5-thinking-agentic` | Both thinking + agentic |

**Suffixes:**
- `-thinking`: Enable adaptive thinking with reasoning display
- `-agentic`: Enable file operation tools with chunked write protocol

### Tool Calls

farouter supports OpenAI-format tool calls with automatic conversion to Kiro format:

```json
{
  "model": "kr/claude-sonnet-4.5",
  "messages": [...],
  "tools": [
    {
      "type": "function",
      "function": {
        "name": "get_weather",
        "description": "Get weather for a location",
        "parameters": {
          "type": "object",
          "properties": {
            "location": {"type": "string"}
          },
          "required": ["location"]
        }
      }
    }
  ]
}
```

**Features:**
- Orphaned tool results reconciliation (checks entire history, not just previous message)
- MCP nested tool detection
- Tool documentation injection when threshold exceeded (10KB)
- Invalid tool call repair with retry

### Images

Send images in OpenAI or Claude format:

**OpenAI format:**
```json
{
  "role": "user",
  "content": [
    {"type": "text", "text": "What's in this image?"},
    {"type": "image_url", "image_url": {"url": "data:image/png;base64,..."}}
  ]
}
```

**Claude format:**
```json
{
  "role": "user",
  "content": [
    {"type": "text", "text": "Analyze this"},
    {
      "type": "image",
      "source": {
        "type": "base64",
        "media_type": "image/png",
        "data": "iVBORw0KG..."
      }
    }
  ]
}
```

**Note:** Images only supported for Claude models. HTTP URLs converted to text placeholders.

---

## 🎛️ Configuration Reference

### `config.json`

```json
{
  "password": "dashboard-password",
  "rtkEnabled": false,
  "accounts": [
    {
      "label": "Account Name",
      "refreshToken": "kiro-refresh-token",
      "authMethod": "builderid|google|github|api_key|external_idp|idc",
      "region": "us-east-1|eu-central-1|...",
      "profileArn": "arn:aws:codewhisperer:...",
      "exhausted": false,
      "suspended": false,
      "resetAt": "2026-07-27T00:00:00Z"
    }
  ],
  "activeBatchIds": ["account-1-id", "account-2-id", "account-3-id"]
}
```

**Fields:**
- `password`: Web dashboard login password
- `rtkEnabled`: Enable RTK tool message processing (default: false)
- `accounts[].label`: Display name for the account
- `accounts[].refreshToken`: Kiro OAuth refresh token
- `accounts[].authMethod`: Authentication method
  - `builderid`: AWS Builder ID (default OAuth)
  - `google`: Google OAuth
  - `github`: GitHub OAuth
  - `api_key`: Long-lived API key
  - `external_idp`: Microsoft Entra (Azure AD)
  - `idc`: IAM Identity Center (AWS SSO)
- `accounts[].region`: AWS region for CodeWhisperer endpoint (optional, auto-detected)
- `accounts[].profileArn`: AWS profile ARN (optional, auto-discovered)
- `accounts[].exhausted`: Quota exhausted flag (auto-managed)
- `accounts[].suspended`: Account suspended flag (auto-managed)
- `accounts[].resetAt`: Quota reset timestamp (ISO 8601, auto-managed)
- `activeBatchIds`: IDs of accounts in active rotation (auto-managed)

**Auto-Managed Fields:**
farouter automatically updates `exhausted`, `suspended`, `resetAt`, and `activeBatchIds` based on runtime state. Changes are persisted to `config.json` on updates.

---

## 🔗 VansRouter Alignment

Farouter is fully aligned with **VansRouter's Kiro provider specifications** for maximum compatibility and feature parity.

### Alignment Status

| Component | Status | Details |
|-----------|--------|---------|
| **Thinking Budget** | ✅ Aligned | 16,000 default, 1-32,000 range (web-standard) |
| **Effort Mapping** | ✅ Aligned | minimal/low/medium/high/xhigh/max (Anthropic/Gemini standards) |
| **Stop Normalization** | ✅ Aligned | CamelCase conversion, alias mapping, whitespace collapsing |
| **Stop Disposition** | ✅ Aligned | 7 states: Complete, ToolUse, Length, TerminalRefusal, TerminalIncomplete, RetryableProtocolFail, UnknownFailure |
| **Session Fingerprint** | ✅ Aligned | SHA256 hash for per-account identification |
| **EventStream Protocol** | ✅ Aligned | AWS binary format with CRC32 validation |
| **Overall Compatibility** | ✅ 100% | Production-ready feature parity |

### Changes from Previous Version

**Thinking Budget Constants (Updated):**
- `ThinkingBudgetDefault`: 20,000 → **16,000** tokens
- `ThinkingBudgetMin`: 1,024 → **1** token
- `ThinkingBudgetMax`: 24,576 → **32,000** tokens

**Effort Level Mapping (Updated):**
```
Before (Farouter custom):
  low:    4,000 tokens
  medium: 8,000 tokens
  high:   20,000 tokens

After (VansRouter web-standard):
  minimal: 512 tokens (NEW)
  low:     1,024 tokens
  medium:  8,192 tokens
  high:    24,576 tokens
  xhigh:   32,768 tokens (NEW)
  max:     128,000 tokens (NEW)
```

### Test Coverage

**59 comprehensive alignment tests** verify VansRouter compatibility:
- TestThinkingBudgetConstants (3 tests)
- TestBuildThinkingSystemPrefix (6 tests)
- TestResolveThinkingBudgetEffortLevels (14 tests)
- TestNormalizeStopReasonString (14 tests)
- TestStopDisposition (14 tests)
- TestRefusalMatch (8 tests)

All tests passing ✅ with performance benchmarks confirming no regressions.

---



### Account Rotation Flow

```
┌─────────────────────────────────────────────────────────────┐
│ Active Batch (3 slots)                                      │
│ ┌─────────┐ ┌─────────┐ ┌─────────┐                        │
│ │Account 1│ │Account 2│ │Account 3│ stickyCount: 0-2       │
│ └─────────┘ └─────────┘ └─────────┘                        │
└─────────────────────────────────────────────────────────────┘
         │                   │
         │ exhausted (402)   │ suspended (403)
         ▼                   ▼
┌─────────────────────────────────────────────────────────────┐
│ Standby Queue                                               │
│ [Account 4] → [Account 5] → [Account 6] → ...              │
└─────────────────────────────────────────────────────────────┘
```

**Rotation Logic:**
1. Pick next account from `activeBatch[0:3]` (round-robin)
2. Use same account for 3 requests (sticky routing)
3. On exhaustion (402): replace with standby, reset sticky count
4. On suspension (403): mark suspended, try next account
5. If all exhausted: reset all non-suspended accounts, retry once
6. Auto-reactivate exhausted accounts when `resetAt` timestamp expires

### Request Processing Flow

```
Client Request (OpenAI format)
    ↓
[Auth Middleware] → Session validation
    ↓
[handleChatCompletions] → Parse request
    ↓
[RTK Processing] → Optional tool message processing
    ↓
[Account Selection] → Pick from activeBatch (sticky routing)
    ↓
[Token Refresh] → Check expiry, refresh if needed
    ↓
[buildKiroRequest] → Convert OpenAI → Kiro format
    ├─ Message conversion (tools, images, history)
    ├─ Orphaned tool result reconciliation
    ├─ Session replay (if conversationId matches)
    └─ Profile ARN discovery (if missing)
    ↓
[Execute] → Send to Kiro upstream
    ├─ Region-aware endpoint selection
    ├─ Endpoint failover (amazonaws.com → kiro.dev)
    └─ Retry with delay (max 2 retries)
    ↓
[Integrity Check] → Validate response completeness
    ├─ Ellipsis detection
    ├─ Short future action detection
    ├─ Invalid tool call detection
    └─ If failed: retry with repair instruction
    ↓
[SSE Transform] → EventStream → OpenAI SSE
    ├─ Parse Kiro events (assistantResponseEvent, toolUseEvent, etc)
    ├─ Extract thinking content (separate from assistant message)
    ├─ Parse quota/usage from meteringEvent
    └─ Stream to client with heartbeat keepalive
    ↓
Client Response (OpenAI SSE format)
```

### Component Overview

| Component | File | Purpose |
|-----------|------|---------|
| **Main Server** | `main.go` | HTTP server, routing, account management, config persistence |
| **Executor** | `executor.go` | Request execution, retry logic, integrity validation |
| **Transform** | `transform.go` | Message conversion (OpenAI ↔ Kiro), tool handling, image processing |
| **SSE Handler** | `kirohandler.go` | Kiro EventStream → OpenAI SSE transformation |
| **SSE Pipe** | `ssepipe.go` | SSE passthrough with thinking extraction, usage tracking |
| **Integrity** | `integrity.go` | Response validation, repair detection (ellipsis, short final, invalid tool) |
| **Client** | `client.go` | HTTP client, endpoint failover, region handling, profile discovery |
| **Token** | `token.go` | Token refresh, expiry check |
| **Quota** | `quota.go` | Quota parsing from Kiro metering events |
| **Models** | `models.go` | Model resolution (agentic/thinking flags), profile ARN defaults |
| **Session** | `session.go` | Session replay for conversation continuity |
| **Types** | `types.go` | Request/response types, constants |

---

## 🔍 Integrity Validation

farouter includes sophisticated response integrity validation to detect and repair incomplete Kiro responses:

### Detection Categories

1. **Ellipsis-only responses** (`...` or `…`)
   - Kiro sometimes returns only ellipsis when model stalls
   - Auto-detected and repaired with full response retry

2. **Short future action announcements**
   - Detects when model announces future action but doesn't complete it
   - Examples: "I'll check that now", "Let me verify", "接下來我會確認"
   - Multi-language regex patterns (English, Chinese)
   - Checks for:
     - Future action keywords
     - Absence of result evidence
     - Absence of completed work indicators
     - Not waiting for user input

3. **Invalid tool calls**
   - Malformed `tool_call` wrapper (missing name/arguments)
   - Nested MCP tool format issues
   - Auto-repairs by instructing model to use correct format

### Repair Process

When integrity check fails:
1. Buffer full response (up to 8MB)
2. Analyze terminal state (stop reason, content, tool calls)
3. Classify failure type (ellipsis / short_final / invalid_tool)
4. Inject repair instruction into system prompt
5. **Start heartbeat** (10s interval to keep connection alive)
6. Retry request with repair instruction
7. Stream repaired response to client
8. If retry fails: return original response with diagnostics

**Repair Instructions:**
- `ellipsis`: "Return the complete final answer, not only ... or …"
- `short_final`: "Complete the check now and return the result or a concrete blocker"
- `tool`: "Use correct tool_call wrapper with non-empty name and arguments field"

---

## 📊 Web Dashboard

Access at `http://localhost:20180` after login.

**Features:**
- **Account overview**: Status, label, auth method, quota remaining
- **Active batch**: 3 accounts in rotation with sticky count
- **Standby queue**: Remaining accounts waiting for rotation
- **Manual actions**:
  - Add new account
  - Remove account
  - Refresh token manually
  - Reset exhausted accounts
  - Toggle RTK processing
- **Real-time updates**: Status changes reflect immediately

**Tech Stack:**
- React 19 with React Router v8
- Tailwind CSS v4 (CSS-first, no PostCSS)
- Brotli-compressed assets (level 11, ~70% size reduction)
- Session-based auth with HTTP-only cookies

---

## 🧪 Testing

Run tests:
```bash
cd internal/kiro
go test -v
```

**Test Coverage:**
- Message JSON deserialization
- SSE line parsing
- Orphaned tool results reconciliation
- Tool call validation
- Integrity validation logic

### VansRouter Alignment Tests

Farouter has been aligned with VansRouter's Kiro provider specifications. Run alignment verification tests:

```bash
cd internal/kiro
go test -v -run "TestThinkingBudgetConstants|TestBuildThinkingSystemPrefix|TestResolveThinkingBudgetEffortLevels|TestNormalizeStopReasonString|TestStopDisposition"
```

**59 comprehensive tests** verify alignment across:
- ✅ Thinking budget constants (16k default, 1-32k range)
- ✅ Effort level mapping (web-standard: minimal, low, medium, high, xhigh, max)
- ✅ Stop reason normalization (camelCase, aliasing, collapsing)
- ✅ Stop disposition mapping (7 states)
- ✅ Refusal pattern detection
- ✅ Ellipsis detection

**All 59 tests passing** with no performance regressions.

---

## 🛠️ Development

### Project Structure

```
farouter/
├── main.go                      # HTTP server, routing, account management
├── go.mod                       # Go dependencies
├── config.json                  # Runtime configuration (auto-managed)
├── config.example.json          # Configuration template
├── internal/kiro/              # Kiro integration package
│   ├── executor.go             # Request execution, retry, integrity
│   ├── transform.go            # Message conversion (OpenAI ↔ Kiro)
│   ├── kirohandler.go          # EventStream → SSE transformation
│   ├── ssepipe.go              # SSE passthrough, usage tracking
│   ├── integrity.go            # Integrity validation, repair detection
│   ├── client.go               # HTTP client, failover, region handling
│   ├── token.go                # Token refresh
│   ├── quota.go                # Quota parsing
│   ├── models.go               # Model resolution
│   ├── session.go              # Session replay
│   ├── types.go                # Request/response types
│   ├── parser.go               # EventStream binary parser
│   ├── sanitizer.go            # Tool sanitization
│   ├── stream.go               # Stream utilities
│   ├── errors.go               # Error types
│   ├── config.go               # Config management
│   ├── service.go              # Service utilities
│   ├── *_test.go               # Test files
│   └── transform_test.go       # Orphaned tool results tests
├── internal/rtk/               # RTK tool processing (optional)
│   ├── rtk.go
│   └── rtk_test.go
└── web/                        # React dashboard (embedded)
    ├── src/
    │   ├── components/
    │   ├── routes/
    │   └── App.jsx
    ├── public/
    └── package.json
```

### Build Web Assets

```bash
cd web
npm install
npm run build  # Outputs to public/ with .br compression
```

Assets are embedded into Go binary at compile time via `//go:embed`.

### Environment Variables

- `KIRO_TOOL_CALL_REPAIR_BUFFER_MAX_BYTES`: Max buffer size for integrity validation (default: 8MB)
- `KIRO_TOOL_CALL_REPAIR_TTFT_TIMEOUT_MS`: Time-to-first-token timeout (default: 30s)
- `KIRO_TOOL_CALL_REPAIR_STALL_TIMEOUT_MS`: Stall timeout during streaming (default: 30s)
- `KIRO_TOOL_CALL_REPAIR`: Set to `"false"` to disable integrity repair

---

## 🔐 Security

- **Session-based auth** with HTTP-only cookies (32-byte random tokens)
- **Password-protected** web dashboard (configurable in `config.json`)
- **No credential logging** (tokens/passwords never logged)
- **Auto token refresh** (no manual token management needed)
- **CORS enabled** for `/v1/*` endpoints (dashboard auth still required)

**Best Practices:**
- Use strong passwords for dashboard access
- Store `config.json` securely (contains refresh tokens)
- Run behind reverse proxy (nginx/caddy) for HTTPS in production
- Rotate refresh tokens periodically

---

## 🚦 Status Codes

| Code | Meaning | Action |
|------|---------|--------|
| 200 | Success | Request completed |
| 400 | Bad Request | Invalid request format |
| 401 | Unauthorized | Missing/invalid session token |
| 402 | Payment Required | Account quota exhausted (auto-rotates to standby) |
| 403 | Forbidden | Account suspended (tries next account) |
| 500 | Internal Error | Server error (check logs) |
| 503 | Service Unavailable | All accounts exhausted/suspended |

**Exhaustion Handling:**
- 402 from Kiro → mark account exhausted → replace from standby → retry
- If all exhausted → reset non-suspended accounts → retry once → 503 if still failing

**Suspension Handling:**
- 403 with `TEMPORARILY_SUSPENDED` → mark account suspended → try next account
- Suspended accounts **NOT** auto-reset (require manual intervention or Kiro support)

---

## 📝 Changelog

### Recent Improvements

**Response Integrity:**
- ✅ Added heartbeat keepalive during repair (10s interval)
- ✅ Improved orphaned tool results detection (check all history, not just prev message)
- ✅ Added region-aware endpoint routing for IAM Identity Center tokens

**Account Management:**
- ✅ Sticky routing (3 requests per account before rotation)
- ✅ Auto-reset exhausted accounts when quota resets
- ✅ Background token refresh (every 6 hours)

**Web Dashboard:**
- ✅ Upgraded to React 19 + React Router v8
- ✅ Tailwind v4 CSS-first (no PostCSS)
- ✅ Brotli compression for optimized delivery

**Testing:**
- ✅ Added comprehensive tests for orphaned tool results reconciliation
- ✅ All tests passing (6/6 for transform logic)

---

## 🤝 Contributing

Contributions welcome! Please:
1. Fork the repo
2. Create feature branch (`git checkout -b feature/amazing-feature`)
3. Commit changes (`git commit -m 'Add amazing feature'`)
4. Push to branch (`git push origin feature/amazing-feature`)
5. Open Pull Request

**Guidelines:**
- Follow existing code style
- Add tests for new features
- Update README if adding user-facing features
- Keep commits atomic and well-described

---

## 📄 License

MIT License - see LICENSE file for details

---

## 🙏 Acknowledgments

- Built on AWS CodeWhisperer (Kiro) infrastructure
- Aligned with VansRouter/9router architecture
- Uses Go's excellent standard library (minimal dependencies)
- React dashboard powered by modern web standards

---

## 📞 Support

**Issues:** [GitHub Issues](https://github.com/your-repo/farouter/issues)

**Common Issues:**

**Q: All accounts showing exhausted?**
A: Check quota reset times in dashboard. Wait for reset or add more accounts to standby queue.

**Q: 403 Forbidden on all accounts?**
A: Accounts may be suspended by Kiro. Check account status in Kiro IDE. Contact AWS support if needed.

**Q: Token refresh failing?**
A: Refresh tokens may have expired. Re-authenticate in Kiro IDE and extract new refresh token.

**Q: Web dashboard not loading?**
A: Check that port 20180 is accessible. Verify `web/public/*.br` files exist in build.

**Q: Images not working?**
A: Images only supported for Claude models. Ensure model name contains "claude".

**Q: Integrity repair not working?**
A: Check that `KIRO_TOOL_CALL_REPAIR` env var is not set to "false". Verify buffer size is sufficient.

---

**Built with ❤️ for developers who need reliable AI routing**
