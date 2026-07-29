# farouter

High-performance Go proxy for Kiro accounts. Manages rotation, token refresh, quota tracking, integrity validation, and web dashboard.

---

## Features

- **Account rotation**: 3-slot active batch + standby queue for unlimited accounts
- **Sticky routing**: 3 requests per account before rotation
- **Auto failover**: Handles exhaustion (402) and suspension (403)
- **Token refresh**: Background keepalive every 6 hours
- **Auth methods**: OAuth (Builder ID, Google, GitHub), API keys, External IdP, IAM Identity Center
- **Integrity validation**: Auto-repair incomplete responses, ellipsis detection, short future action detection
- **Region-aware routing**: Auto-regionalization based on token region
- **RTK processing**: Optional tool message processing (configurable)
- **Images**: OpenAI and Claude formats supported
- **Thinking mode**: Adaptive effort levels (minimal/low/medium/high/xhigh/max)
- **Agentic mode**: Chunked write protocol for file operations
- **Web dashboard**: React 19 + Tailwind v4 with real-time status
- **Zero external deps**: Pure Go with embedded web assets (~8MB)

---

## Quick Start

### Prerequisites
- Go 1.21+ (build from source) OR download pre-built binary

### Install

```bash
# Build from source
cd farouter
go build -o farouter main.go

# Or download binary
wget https://github.com/your-repo/farouter/releases/latest/download/farouter
chmod +x farouter
```

### Configure

Create `config.json`:

```json
{
  "password": "secure-password",
  "rtkEnabled": false,
  "accounts": [
    {
      "label": "Account 1",
      "refreshToken": "your-kiro-refresh-token",
      "authMethod": "builderid",
      "region": "us-east-1"
    }
  ]
}
```

**Get Kiro refresh tokens:**
- Extract from Kiro IDE DevTools in browser
- Or use OAuth flow to authenticate

**Start server:**
```bash
./farouter
```

Access at:
- API: `http://localhost:20180`
- Dashboard: `http://localhost:20180` (login with password)

---

## Usage

### OpenAI-Compatible API

```bash
curl -X POST http://localhost:20180/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "kr/claude-sonnet-4.5",
    "messages": [{"role": "user", "content": "Hello!"}],
    "stream": true
  }'
```

### Supported Models

| Model | Description |
|-------|-------------|
| `kr/auto` | Auto-select best model |
| `kr/claude-sonnet-4.5` | Base model |
| `kr/claude-sonnet-4.5-thinking` | With thinking |
| `kr/claude-sonnet-4.5-agentic` | With agentic tools |
| `kr/claude-sonnet-4.5-thinking-agentic` | Both thinking + agentic |

**Suffixes:**
- `-thinking`: Enable adaptive thinking
- `-agentic`: Enable file operation tools

### Tool Calls

Auto-converts OpenAI format to Kiro:

```json
{
  "model": "kr/claude-sonnet-4.5",
  "messages": [...],
  "tools": [
    {
      "type": "function",
      "function": {
        "name": "get_weather",
        "description": "Get weather",
        "parameters": {
          "type": "object",
          "properties": {"location": {"type": "string"}},
          "required": ["location"]
        }
      }
    }
  ]
}
```

**Features:**
- Orphaned tool results reconciliation
- MCP nested tool detection
- Tool documentation injection (>10KB threshold)
- Invalid tool call auto-repair

### Images

Send images in OpenAI or Claude format:

**OpenAI format:**
```json
{
  "role": "user",
  "content": [
    {"type": "text", "text": "What's in this?"},
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

**Note:** Images only work with Claude models. HTTP URLs converted to text placeholders.

---

## Configuration

### config.json Fields

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

**Auto-managed:** `exhausted`, `suspended`, `resetAt`, `activeBatchIds`

---

## 🔗 VansRouter Alignment

Farouter is fully aligned with VansRouter's Kiro provider specifications.

### Status

| Component | Status | Details |
|-----------|--------|---------|
| **Thinking Budget** | ✅ | 16,000 default, 1-32,000 range |
| **Effort Mapping** | ✅ | minimal/low/medium/high/xhigh/max |
| **Stop Normalization** | ✅ | CamelCase, aliasing, collapsing |
| **Stop Disposition** | ✅ | 7 states: Complete, ToolUse, Length, TerminalRefusal, TerminalIncomplete, RetryableProtocolFail, UnknownFailure |
| **Session Fingerprint** | ✅ | SHA256 hash per-account |
| **EventStream Protocol** | ✅ | AWS binary + CRC32 validation |

### Thinking Budget Changes

```
Before (Farouter custom):
  Default: 20,000 → 16,000
  Min:     1,024  → 1
  Max:     24,576 → 32,000

Effort mapping:
  low:     4,000   → 1,024
  medium:  8,000   → 8,192
  high:    20,000  → 24,576
  xhigh:   -       → 32,768 (NEW)
  minimal: -       → 512 (NEW)
  max:     -       → 128,000 (NEW)
```

### Alignment Tests

**59 comprehensive tests** verify VansRouter compatibility:

```bash
cd internal/kiro
go test -v -run "TestThinkingBudgetConstants|TestBuildThinkingSystemPrefix|TestResolveThinkingBudgetEffortLevels|TestNormalizeStopReasonString|TestStopDisposition"
```

All passing ✅ with no performance regressions.

---

## Architecture

### Account Rotation

```
Active Batch (3 slots)
├─ Account 1
├─ Account 2
└─ Account 3 (stickyCount: 0-2)
   
   │ 402 (exhausted)    │ 403 (suspended)
   ▼                    ▼

Standby Queue
├─ Account 4
├─ Account 5
└─ Account 6 ...
```

**Logic:**
1. Pick next account from `activeBatch` (round-robin)
2. Use same account for 3 requests (sticky)
3. On 402: replace with standby, reset sticky count
4. On 403: mark suspended, try next
5. If all exhausted: reset non-suspended, retry once
6. Auto-reactivate when `resetAt` expires

### Request Flow

```
Client Request (OpenAI format)
  ↓
[Auth] → Session validation
  ↓
[Parse] → Extract request
  ↓
[RTK] → Optional tool processing
  ↓
[Select Account] → Pick from activeBatch (sticky)
  ↓
[Refresh Token] → Check/refresh if needed
  ↓
[Build Kiro Request] → Convert OpenAI → Kiro format
  ├─ Message conversion
  ├─ Tool reconciliation
  ├─ Image handling
  ├─ Session replay
  └─ Profile ARN discovery
  ↓
[Execute] → Send to Kiro
  ├─ Region-aware endpoint
  ├─ Failover (amazonaws.com → kiro.dev)
  └─ Retry (max 2)
  ↓
[Integrity Check] → Validate completeness
  ├─ Ellipsis detection
  ├─ Short action detection
  ├─ Invalid tool call detection
  └─ Repair if needed
  ↓
[Transform] → Kiro EventStream → OpenAI SSE
  ├─ Parse events
  ├─ Extract thinking
  ├─ Parse quota/usage
  └─ Stream with keepalive
  ↓
Client Response (OpenAI SSE format)
```

### Components

| Component | File | Purpose |
|-----------|------|---------|
| Server | `main.go` | HTTP routing, account mgmt, config |
| Executor | `executor.go` | Execute, retry, integrity validation |
| Transform | `transform.go` | OpenAI ↔ Kiro conversion |
| SSE Handler | `kirohandler.go` | EventStream → SSE |
| SSE Pipe | `ssepipe.go` | SSE passthrough, usage tracking |
| Integrity | `integrity.go` | Validation, repair detection |
| Client | `client.go` | HTTP client, failover, region |
| Token | `token.go` | Token refresh |
| Quota | `quota.go` | Parse Kiro metering |
| Models | `models.go` | Model resolution |
| Session | `session.go` | Session replay |
| Types | `types.go` | Request/response types |

---

## Integrity Validation

Auto-detects and repairs incomplete responses.

### Detection

1. **Ellipsis-only**: `...` or `…` with no content
2. **Short future action**: "I'll check now", "Let me verify", etc. without result
3. **Invalid tool calls**: Malformed `tool_call` wrapper

### Repair Process

1. Buffer response (up to 8MB)
2. Analyze terminal state
3. Classify failure type
4. Inject repair instruction
5. Start heartbeat (10s)
6. Retry with repair instruction
7. Stream repaired response
8. Fallback to original if retry fails

**Repair instructions:**
- `ellipsis`: "Return complete answer, not only ... or …"
- `short_final`: "Complete now and return result or blocker"
- `tool`: "Use correct tool_call wrapper with name and arguments"

---

## Web Dashboard

Access at `http://localhost:20180` after login.

**Features:**
- Account overview (status, quota, auth method)
- Active batch (3 accounts + sticky count)
- Standby queue (remaining accounts)
- Manual actions (add, remove, refresh token, reset)
- Real-time updates

**Tech:**
- React 19 + React Router v8
- Tailwind v4 (CSS-first)
- Brotli compression (level 11, ~70% reduction)
- Session-based auth (HTTP-only cookies)

---

## Testing

```bash
cd internal/kiro
go test -v
```

**Coverage:**
- Message deserialization
- SSE parsing
- Tool results reconciliation
- Tool validation
- Integrity logic

---

## Development

### Structure

```
farouter/
├── main.go
├── go.mod
├── config.json (auto-managed)
├── config.example.json
├── internal/kiro/
│   ├── executor.go
│   ├── transform.go
│   ├── kirohandler.go
│   ├── ssepipe.go
│   ├── integrity.go
│   ├── client.go
│   ├── token.go
│   ├── quota.go
│   ├── models.go
│   ├── session.go
│   ├── types.go
│   ├── parser.go
│   ├── sanitizer.go
│   ├── stream.go
│   ├── errors.go
│   ├── config.go
│   ├── service.go
│   ├── *_test.go
│   └── corrections_test.go
├── internal/rtk/
│   ├── rtk.go
│   └── rtk_test.go
└── web/
    ├── src/
    ├── public/
    └── package.json
```

### Build Web Assets

```bash
cd web
npm install
npm run build  # Outputs to public/ with .br compression
```

Assets embedded at compile time via `//go:embed`.

### Environment Variables

- `KIRO_TOOL_CALL_REPAIR_BUFFER_MAX_BYTES`: Max buffer (default: 8MB)
- `KIRO_TOOL_CALL_REPAIR_TTFT_TIMEOUT_MS`: TTFT timeout (default: 30s)
- `KIRO_TOOL_CALL_REPAIR_STALL_TIMEOUT_MS`: Stall timeout (default: 30s)
- `KIRO_TOOL_CALL_REPAIR`: Set to `"false"` to disable repair

---

## Security

- Session-based auth (HTTP-only cookies, 32-byte random tokens)
- Password-protected dashboard
- No credential logging
- Auto token refresh
- CORS enabled for `/v1/*`

**Best practices:**
- Use strong passwords
- Store `config.json` securely (contains refresh tokens)
- Run behind reverse proxy (nginx/caddy) for HTTPS
- Rotate refresh tokens periodically

---

## Status Codes

| Code | Meaning | Action |
|------|---------|--------|
| 200 | Success | Done |
| 400 | Bad Request | Invalid format |
| 401 | Unauthorized | Missing/invalid session |
| 402 | Payment Required | Quota exhausted → rotate |
| 403 | Forbidden | Suspended → try next |
| 500 | Internal Error | Check logs |
| 503 | Service Unavailable | All exhausted/suspended |

**Exhaustion:** 402 → mark exhausted → replace from standby → retry
**Suspension:** 403 → mark suspended → try next (manual reset needed)

---

## Contributing

1. Fork repo
2. Create feature branch
3. Add tests
4. Update README if user-facing
5. Keep commits atomic
6. Open PR

---

## License

MIT License - see LICENSE file for details

---

**Aligned with VansRouter. Built for reliable AI routing.**
