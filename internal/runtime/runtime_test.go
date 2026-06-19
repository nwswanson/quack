package runtime

import (
	"context"
	"errors"
	"testing"
)

func TestDisabledServiceDoesNotInvokeRuntime(t *testing.T) {
	service := NewDisabledService()

	_, err := service.InvokeHTTP(context.Background(), InvocationRequest{
		Site: "foo", Version: 1, Route: "/api", Method: "GET",
	})
	if !errors.Is(err, ErrDisabled) {
		t.Fatalf("InvokeHTTP error = %v, want ErrDisabled", err)
	}
}
