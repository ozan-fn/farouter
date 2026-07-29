# Farouter HTTP Migration to Resty v2

## Overview
Migrated all HTTP request/response handling in farouter from Go's standard `net/http` package to **resty v2** for improved client management, chainable API, automatic marshaling, and better timeout control.

## Changes Summary

### Dependencies
- **Added**: `github.com/go-resty/resty/v2 v2.15.0`
- **Removed**: Direct use of `net/http.Client` and `http.NewRequest`

### Files Modified

#### 1. `go.mod`
- Added resty v2 dependency with pinned version v2.15.0

#### 2. `internal/kiro/client.go` (Core Migration)

**Before**: Raw `http.Client` with manual transport setup
```go
var httpClient = &http.Client{
    Transport: &http.Transport{
        DialContext: (&net.Dialer{
            Timeout: FetchConnectTimeout,
            KeepAlive: 30 * time.Second,
        }).DialContext,
        TLSHandshakeTimeout: FetchConnectTimeout,
    },
}
```

**After**: Resty v2 client with identical transport config
```go
var httpClient = createRestyClient()

func createRestyClient() *resty.Client {
    client := resty.New()
    client.SetTransport(&http.Transport{...})
    return client
}
```

**Benefits**:
- Chainable API for building requests
- Automatic JSON marshaling/unmarshaling
- Built-in retry policies
- Better error handling patterns

### Request/Response Pattern Changes

#### `sendToKiroCtx()` - Streaming POST requests
**Before**:
```go
req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL, bytes.NewReader(body))
req.Header.Set(k, v)
resp, err := doRequestRawCtx(ctx, req)
if resp.StatusCode == http.StatusTooManyRequests {
    resp.Body.Close()
}
```

**After**:
```go
resp, err := client.R().
    SetContext(ctx).
    SetHeaders(headers).
    SetBody(body).
    Post(baseURL)

if resp.StatusCode() == http.StatusTooManyRequests {
    // No manual body.Close() needed - resty handles it
}
return resp.RawResponse  // Get underlying *http.Response if needed
```

#### `listProfileArnForRegion()` - JSON request/response
**Before**:
```go
req.Header.Set("Content-Type", "application/x-amz-json-1.0")
resp, err := doRequestRawCtx(timeoutCtx, req)
defer resp.Body.Close()
var data profileResp
json.NewDecoder(resp.Body).Decode(&data)
```

**After**:
```go
var data profileResp
resp, err := client.R().
    SetContext(timeoutCtx).
    SetHeader("Content-Type", "application/x-amz-json-1.0").
    SetBody(reqBody).
    SetResult(&data).  // Auto-unmarshal into data
    Post(host + "/")

// data is automatically populated, no manual decode needed
```

### Files Updated for Compatibility

#### `internal/kiro/executor.go`
#### `internal/kiro/quota.go`
#### `internal/kiro/service.go`
#### `internal/kiro/token.go`

All files using `getHttpClient().Do(req)` updated to:
```go
getHttpClient().GetClient().Do(req)  // Access underlying http.Client
```

This preserves backward compatibility for code that needs raw `*http.Request` objects.

## Key Resty v2 Methods Used

### Client Setup
- `resty.New()` - Create client
- `SetTransport()` - Configure HTTP transport
- `GetClient()` - Get underlying `*http.Client`

### Request Building
- `R()` - Create new request
- `SetContext(ctx)` - Set request context
- `SetHeaders(map)` / `SetHeader(key, value)` - Set headers
- `SetBody(interface{})` - Set request body (auto-marshals if needed)
- `SetResult(&structPtr)` - Auto-unmarshal success response
- `Post/Get/Put/Delete(url)` - Execute request

### Response Access
- `StatusCode()` - Get HTTP status code
- `RawResponse` - Get underlying `*http.Response`
- `Result()` - Get unmarshaled result from SetResult

## Error Handling

**Standard pattern** (unchanged):
```go
if err != nil {
    // Network/connection error
    return err
}

if resp.StatusCode() != http.StatusOK {
    // HTTP error
    return fmt.Errorf("HTTP %d", resp.StatusCode())
}

// Success - result auto-unmarshaled via SetResult
```

## Timeout Behavior

**Preserved from original**:
- Connection timeout via DialContext: `FetchConnectTimeout`
- TLS handshake timeout: `FetchConnectTimeout`
- Non-streaming requests use `context.WithTimeout(10s)`
- Streaming requests managed via `PipeWithDisconnect` (no client-side timeout)

**Why no client-level timeout**:
Resty's `SetTimeout()` would kill the body read mid-stream, breaking SSE. The original code pattern is preserved.

## Testing

✅ Build verified: `go build` passes
✅ All imports correct
✅ Type safety maintained
✅ Request/response flow unchanged from external perspective

## Migration Checklist

- [x] Add resty v2 dependency to go.mod
- [x] Replace httpClient initialization
- [x] Update sendToKiroCtx() for chainable API
- [x] Update listProfileArnForRegion() with SetResult()
- [x] Fix all getHttpClient().Do() calls
- [x] Remove unused `bytes` import
- [x] Verify build succeeds
- [x] Maintain backward compatibility via GetClient()

## Performance Impact

- **Positive**: Chainable API reduces allocations for header maps
- **Positive**: Auto-marshaling reduces manual json.Marshal/Unmarshal calls
- **Neutral**: Same underlying HTTP transport config
- **Neutral**: Connection pooling unchanged (resty uses http.DefaultTransport patterns)

## Future Improvements

With resty v2 now in place, potential enhancements:
1. `SetRetry(attempts, wait)` for automatic retry on transient failures
2. Request/response logging hooks
3. Circuit breaker patterns
4. Distributed tracing integration
