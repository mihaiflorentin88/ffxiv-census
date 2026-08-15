# HTTP Client

This project includes a small outbound HTTP client contract and adapter built on top of `net/http`.

## Resolve The Client

Container wiring for this feature is added separately. Once that wiring is in place, resolve the client from the service container:

```go
client := container.Load.HTTPClient()
```

Until then, construct the adapter directly:

```go
client := httpclient.New(nil)
```

Passing `nil` uses a default `*http.Client` with a 30-second timeout. You can inject your own `*http.Client` when you need custom transports, tracing, or tighter test control.

## Request And Response DTOs

The generated request and response types live under `port/dto/request` and `port/dto/response`.

- `request.HTTPRequest` keeps outbound request data transport-focused: method, URL, query params, headers, body, and an optional per-request timeout.
- `response.HTTPResponse` returns structured transport data: status code, headers, and body.

These DTOs stay intentionally small so application code can use a stable contract without turning the boilerplate into a full SDK.

## Usage

```go
resp, err := client.Get(
	ctx,
	"https://api.example.com/v1/users",
	map[string]string{"page": "1"},
	map[string]string{"Accept": "application/json"},
)
if err != nil {
	return err
}

created, err := client.Post(
	ctx,
	"https://api.example.com/v1/users",
	nil,
	map[string]string{"Content-Type": "application/json"},
	[]byte(`{"name":"demo"}`),
)
if err != nil {
	return err
}

_ = resp
_ = created
```

All convenience methods delegate to `Do`. Non-2xx responses return a descriptive error and still provide the structured response payload when available.
