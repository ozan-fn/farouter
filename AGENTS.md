# AGENTS.md — farouter

## Source Tracking

farouter mengimplementasikan ulang logika routing dari **9router** dalam Go.
Saat ada update di 9router, cek file-file berikut dan sinkronkan ke farouter.

### 9router Commit Terakhir yang Disync

```
commit: 79918c7830695bbca4a45c9fea4a42c3e9fd73d1
date:   2026-07-20 17:21:41 +0700
tag:    v0.5.40 (2026-07-20)
author: decolua
```

### File 9router yang Relevan → File farouter

| 9router | farouter |
|---|---|
| `open-sse/translator/request/openai-to-kiro.js` | `internal/kiro/executor.go` (convertMessages, buildKiroRequest) |
| `open-sse/config/kiroConstants.js` | `internal/kiro/models.go` |
| `open-sse/executors/kiro.js` | `internal/kiro/executor.go` (sendToKiro), `internal/kiro/sse.go` (transformEventStreamToSSE) |
| `open-sse/utils/kiroSessionReplay.js` | `internal/kiro/session.go` (applySessionReplay) |
| `src/lib/oauth/services/kiro.js` | `internal/kiro/token.go` |
| `open-sse/providers/registry/kiro.js` | `internal/kiro/executor.go` (baseURLs, headers), `internal/kiro/models.go` (KnownModels) |
| `keirouter/backend/internal/connectors/kiro.go` | `internal/kiro/quota.go` (FetchQuota, parseQuota) |

### Cara Cek Update 9router

```bash
cd /home/ozan/projects/router/9router
git log --oneline -10
git diff 79918c7..HEAD -- open-sse/translator/request/openai-to-kiro.js open-sse/config/kiroConstants.js open-sse/executors/kiro.js open-sse/utils/kiroSessionReplay.js src/lib/oauth/services/kiro.js open-sse/providers/registry/kiro.js
```

### Yang Harus Diperhatikan Saat Update

- **Request format** — `convertMessages` dan `buildKiroRequest` harus selalu sinkron dengan `openai-to-kiro.js`
- **Model list** — model baru di `kiro.js` registry perlu ditambah ke `models.go` `KnownModels`
- **Token refresh** — endpoint di `token.go` harus sinkron dengan `src/lib/oauth/services/kiro.js`
- **EventStream events** — event type baru di `kiro.js` executor perlu dihandle di `sse.go`
- **baseURLs** — kalau 9router tambah endpoint baru, update `baseURLs` di `executor.go`
- **Headers** — headers di `sendToKiro` harus sinkron dengan `kiro.js` `buildHeaders`
- **Quota** — field quota response bisa berubah, sinkronkan `parseQuota` di `quota.go` dengan `keirouter/backend/internal/connectors/kiro.go`

### Fitur farouter (tidak ada di 9router)

- Multi-account rotation dengan sticky 3 request per akun
- Boot quota check — akun exhausted/suspended di-skip otomatis
- Persist state (`exhausted`, `suspended`, `resetAt`, `stickyId`) ke `config.json`
- Auto-aktif kembali saat `resetAt` tercapai
- `GET /status` — lihat state semua akun
- `POST /accounts/reset` — manual reset semua akun
- Retry otomatis ke akun lain saat 402/403-suspended
