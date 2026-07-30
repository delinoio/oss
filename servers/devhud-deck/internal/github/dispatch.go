package github

import "context"

type dispatchObserverContextKey struct{}

// WithDispatchObserver binds provider dispatch accounting to the active
// request. The observer runs immediately before an HTTP RoundTrip; returning
// an error prevents the provider request.
func WithDispatchObserver(
	ctx context.Context,
	observer func() error,
) context.Context {
	if observer == nil {
		return ctx
	}
	return context.WithValue(ctx, dispatchObserverContextKey{}, observer)
}

func notifyDispatch(ctx context.Context) error {
	observer, _ := ctx.Value(dispatchObserverContextKey{}).(func() error)
	if observer == nil {
		return nil
	}
	return observer()
}
