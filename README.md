# farouter

Go-native Kiro router with multi-account rotation, quota tracking, and web dashboard.

---

## Features

- **Multi-account rotation** — sticky 3-request pooling across accounts
- **Boot quota check** — auto-skip exhausted/suspended accounts
- **Persistent state** — `exhausted`, `suspended`, `resetAt`, `stickyId` saved to `config.json`
- **Auto-reactivation** — accounts reactivate when `resetAt` reached
- **Web dashboard** — React 19 + Tailwind v4 + React Router v8
- **Auth** — password-based login with 24h session token
- **Brotli precompression** — embedded frontend with `.br` assets (level 11)
- **Single binary deployment** — `embed.FS` includes web assets

---

## API Endpoints

### Public

- `POST /v1/chat/completions` — OpenAI-compatible chat completions (proxied to Kiro)
- `POST /api/login` — dashboard login (`{"password": "..."}`)
- `GET /api/verify` — check session validity

### Protected (requires auth)

- `GET /status` — view all account states
- `POST /accounts/reset` — reset all non-suspended accounts
- `POST /auth/kiro/refresh` — refresh Kiro tokens

---

## Configuration

`config.json`:

```json
{
  "password": "your-dashboard-password",
  "activeBatchIds": [],
  "currentSlot": 0,
  "stickyCount": 0,
  "accounts": [
    {
      "id": "uuid",
      "label": "Account 1",
      "refreshToken": "aorAAAAAG...",
      "profileArn": "arn:aws:codewhisperer:us-east-1:...",
      "authMethod": "imported"
    }
  ]
}
```

Fields auto-populated at runtime:
- `exhausted` — quota depleted
- `suspended` — 403-suspended by upstream
- `resetAt` — RFC3339 timestamp for auto-reactivation
- `lastRefreshedAt` — last token refresh

---

## Account Rotation Logic

1. **Pool setup** — 3 active accounts (`activeBatch[0:3]`), rest in `standbyQueue`
2. **Sticky requests** — each account serves 3 requests before rotation
3. **Exhaustion** — on 402/quota error, mark exhausted → pull from standby
4. **Suspension** — on 403-suspended, mark suspended → permanent skip
5. **Retry** — on account failure, retry with next account (auto-reset once if all exhausted)
6. **Auto-restore** — accounts with `resetAt` < now reactivate at boot

---

## Deployment

### Build

```bash
cd farouter/web
pnpm install
pnpm run build  # generates web/dist with .br files

cd ..
CGO_ENABLED=0 go build -ldflags="-w -s" -o farouter .
```

### Run

```bash
./farouter
# listening on http://localhost:20180
```

Environment variables:
- `PORT` — server port (default: `20180`)
- `KIRO_INTEGRITY_CHECK=true` — enable Kiro integrity validation

### GitHub Actions

`.github/workflows/deploy.yml` — auto-deploy on push to `main`:
1. Build Go binary with embedded web assets
2. Upload to FTP `/go/farouter` (removes old binary first)
3. Caches: Go build cache, apt packages

---

## Web Dashboard

**Stack**: Rsbuild + React 19 + Tailwind v4 + React Router v8

**Routes**:
- `/` — login page
- `/dashboard` — overview (protected)
- `/dashboard/settings` — settings (protected)

**Auth flow**:
1. User submits password → `POST /api/login`
2. Server validates against `config.json` password
3. On success: 32-byte random token, `httpOnly` cookie + JSON response
4. Frontend stores token, navigates to `/dashboard`
5. React Router `loader` calls `/api/verify` before rendering protected routes

**Build optimization**:
- Brotli level 11 precompression (all `.js`/`.css`/`.html` → `.br`)
- Go server checks `Accept-Encoding: br` → serves `.br` with `Content-Encoding: br`
- SPA fallback: unknown paths serve `index.html`

---

## Source Tracking

farouter re-implements 9router Kiro logic in Go.

**9router commit synced**: `79918c7` (2026-07-20, v0.5.40)

**File mapping**:

| 9router | farouter |
|---------|----------|
| `open-sse/translator/request/openai-to-kiro.js` | `internal/kiro/executor.go` |
| `open-sse/config/kiroConstants.js` | `internal/kiro/models.go` |
| `open-sse/executors/kiro.js` | `internal/kiro/executor.go`, `internal/kiro/sse.go` |
| `open-sse/utils/kiroSessionReplay.js` | `internal/kiro/session.go` |
| `src/lib/oauth/services/kiro.js` | `internal/kiro/token.go` |
| `open-sse/providers/registry/kiro.js` | `internal/kiro/executor.go`, `internal/kiro/models.go` |
| `keirouter/backend/internal/connectors/kiro.go` | `internal/kiro/quota.go` |

**Check for 9router updates**:

```bash
cd /home/ozan/projects/router/9router
git log --oneline -10
git diff 79918c7..HEAD -- open-sse/translator/request/openai-to-kiro.js \
  open-sse/config/kiroConstants.js open-sse/executors/kiro.js
```

**Sync checklist**:
- Request format — `convertMessages` + `buildKiroRequest`
- Model list — update `KnownModels` in `models.go`
- Token refresh — sync endpoint in `token.go`
- EventStream events — handle new types in `sse.go`
- Base URLs — update `baseURLs` in `executor.go`
- Headers — sync `sendToKiro` headers
- Quota fields — update `parseQuota` in `quota.go`

---

## Development

```bash
# Backend
go run .

# Frontend
cd web
pnpm run dev  # http://localhost:5173 (proxies /api to :20180)

# Build
pnpm run build
```

**Project structure**:

```
farouter/
├── main.go              # server, auth, account rotation
├── config.json          # runtime config (git-ignored)
├── config.example.json  # config template
├── internal/kiro/       # Kiro client implementation
│   ├── executor.go      # request conversion, HTTP client
│   ├── sse.go           # EventStream → SSE translation
│   ├── models.go        # model registry
│   ├── token.go         # OAuth token refresh
│   ├── quota.go         # quota API
│   └── session.go       # session replay
└── web/
    ├── src/
    │   ├── pages/       # React Router pages
    │   ├── routes/      # route definitions
    │   └── App.tsx
    ├── rsbuild.config.ts
    └── dist/            # build output (embedded into Go binary)
```

---

## License

MIT
