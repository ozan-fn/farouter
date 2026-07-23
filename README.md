# farouter

OpenAI-compatible reverse proxy untuk Kiro AI (AWS CodeWhisperer) dengan multi-account rotation.

## Setup

```bash
cp config.example.json config.json
# Edit config.json dengan refresh token dari keirouter DB
go build -o farouter .
./farouter
```

## Config

`config.json` diisi otomatis dari `decrypt.go` script atau manual:

```bash
# Decrypt dari keirouter DB
go run decrypt.go master.key keirouter.db
```

## Endpoints

| Method | Path | Deskripsi |
|--------|------|-----------|
| `POST` | `/v1/chat/completions` | OpenAI-compatible chat |
| `POST` | `/auth/kiro/refresh` | Refresh Kiro token |
| `GET` | `/status` | Status semua akun |
| `POST` | `/accounts/reset` | Reset semua akun |

## Models

Format: `kr/<model>` atau `Kafuu/kr/<model>`

| Model | ID |
|-------|----|
| Auto | `kr/auto` |
| Claude Sonnet 4.5 | `kr/claude-sonnet-4.5` |
| Claude Sonnet 5 | `kr/claude-sonnet-5` |
| Claude Opus 4.5 | `kr/claude-opus-4.5` |
| Claude Haiku 4.5 | `kr/claude-haiku-4.5` |

Tambah suffix `-thinking` untuk thinking mode, `-agentic` untuk chunked write protocol.

## Account Rotation

- Sticky 3 request per akun sebelum pindah ke akun berikutnya
- Boot: refresh token + cek quota semua akun parallel
- 402 → mark exhausted, retry ke akun lain
- 403 suspended → mark suspended permanen
- `exhausted` + `resetAt` dipersist ke `config.json`, aktif otomatis saat reset date tercapai

## Environment

| Var | Default | Deskripsi |
|-----|---------|-----------|
| `PORT` | `3000` | Port server |
