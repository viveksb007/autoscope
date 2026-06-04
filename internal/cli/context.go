package cli

import "context"

type globalsKey struct{}

func withGlobals(ctx context.Context, g *GlobalFlags) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, globalsKey{}, g)
}

// FlagsFrom returns the GlobalFlags stored in ctx, or nil if absent.
func FlagsFrom(ctx context.Context) *GlobalFlags {
	v, _ := ctx.Value(globalsKey{}).(*GlobalFlags)
	return v
}
