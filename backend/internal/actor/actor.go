// Package actor identifies who performed an HR write (audit / created_by).
// In AUTH_MODE=dev this comes from X-Dev-User; in oidc from verified claims.
package actor

import "context"

type ctxKey int

const key ctxKey = 1

// Actor is the stamped identity for writes. Not a full authz principal.
type Actor struct {
	Username string
}

// With attaches an Actor to ctx.
func With(ctx context.Context, a *Actor) context.Context {
	return context.WithValue(ctx, key, a)
}

// FromContext returns the Actor if middleware attached one.
func FromContext(ctx context.Context) (*Actor, bool) {
	a, ok := ctx.Value(key).(*Actor)
	if !ok || a == nil || a.Username == "" {
		return nil, false
	}
	return a, true
}

// Name returns Username or fallback.
func Name(ctx context.Context, fallback string) string {
	if a, ok := FromContext(ctx); ok {
		return a.Username
	}
	return fallback
}
