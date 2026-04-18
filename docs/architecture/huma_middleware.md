# Huma & Chi Middleware Architecture

This document explains why Salvia uses specialized Huma middleware (`AuthenticateHuma`) instead of standard Chi middleware for authenticated routes.

## The Context Problem

In a standard Chi application, middleware modifies the `http.Request` context:

```go
// Standard Chi Middleware
func Authenticate(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        ctx := context.WithValue(r.Context(), "user", user)
        next.ServeHTTP(w, r.WithContext(ctx))
    })
}
```

However, **Huma** sits on top of Chi and creates its own `huma.Context`. While Huma's context eventually accesses the underlying request context, the execution order and the way Huma handles "native" Huma handlers means that relying solely on Chi-level context injection can lead to:

1.  **OpenAPI Inaccuracy**: Chi middleware is invisible to the OpenAPI (Swagger) generator. Huma doesn't know the route is protected unless the middleware is registered at the operation level.
2.  **Context Resolution Issues**: Huma handlers receive a `huma.Context`. If the context is modified by Chi *after* Huma has already initialized its own wrapper, the handler might see a stale or incomplete context.

## The Immutability Challenge

`huma.Context` is a Go **interface**, not a struct. Crucially, it provides a `Context()` method to retrieve the underlying `context.Context`, but it **does not** provide a `SetContext()` method.

When we want to inject authentication data (like `ClinicID` or `Permissions`) inside a Huma middleware, we cannot simply update the existing context. We must provide a new context to the next handler.

### The Solution: Context Wrapping

To solve this, we implemented a wrapper pattern in `internal/platform/middleware/auth.go`:

```go
type humaContextWrapper struct {
	HumaContext // Alias for huma.Context
	owCtx context.Context
}

// Override the Context method to return our updated context
func (w *humaContextWrapper) Context() context.Context {
	return w.owCtx
}
```

In the `AuthenticateHuma` middleware, we:
1.  Extract and validate the JWT.
2.  Create a new standard library `context.Context` with the claims.
3.  Wrap the current `hctx` in our `humaContextWrapper`.
4.  Pass the wrapper to the `next()` handler.

## Benefits of this Pattern

1.  **Type Safety**: Huma handlers can reliably use the `mw.StaffIDFromContext(ctx.Context())` helpers because the context is guaranteed to be injected at the Huma layer.
2.  **Explicit Documentation**: Every authenticated endpoint now explicitly lists its security requirements in the code:
    ```go
    huma.Register(api, huma.Operation{
        // ...
        Security:    []map[string][]string{{"bearerAuth": {}}},
        Middlewares: huma.Middlewares{auth},
    }, h.myHandler)
    ```
3.  **Granular Permissions**: We can chain `auth` and `RequirePermissionHuma` effectively, ensuring the UI (Swagger) accurately reflects that an endpoint requires both a valid token AND specific permissions.
