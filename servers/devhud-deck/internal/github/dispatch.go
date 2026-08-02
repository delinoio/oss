package github

import (
	"context"
	"net/http"
)

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

type dispatchTransport struct {
	base http.RoundTripper
}

type dispatchError struct {
	err error
}

func (dispatch *dispatchError) Error() string {
	return dispatch.err.Error()
}

func (dispatch *dispatchError) Unwrap() error {
	return dispatch.err
}

func (transport dispatchTransport) RoundTrip(
	request *http.Request,
) (*http.Response, error) {
	if err := request.Context().Err(); err != nil {
		return nil, err
	}
	if err := notifyDispatch(request.Context()); err != nil {
		return nil, &dispatchError{err: err}
	}
	return transport.base.RoundTrip(request)
}
