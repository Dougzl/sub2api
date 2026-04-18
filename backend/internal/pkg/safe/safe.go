package safe

import (
	"context"
	"log/slog"
	"runtime/debug"
)

// Recover logs and swallows a panic so one bad request/background task cannot
// take down the whole process. Use with defer at goroutine and callback
// boundaries that are not already protected by Gin recovery.
func Recover(component string) {
	if r := recover(); r != nil {
		slog.Error("panic recovered",
			"component", component,
			"panic", r,
			"stack", string(debug.Stack()),
		)
	}
}

// Do runs fn behind a panic guard.
func Do(component string, fn func()) {
	defer Recover(component)
	fn()
}

// Go starts fn in a goroutine with a panic guard.
func Go(component string, fn func()) {
	go func() {
		defer Recover(component)
		fn()
	}()
}

// GoContext starts fn in a goroutine with a panic guard and context.
func GoContext(ctx context.Context, component string, fn func(context.Context)) {
	go func() {
		defer Recover(component)
		fn(ctx)
	}()
}
