package engine

import (
	"errors"
	"fmt"
	"testing"
)

func TestOError_Code(t *testing.T) {
	e := ErrConfig("bad config")
	if e.Error() != "bad config" {
		t.Errorf("Error() = %q, want %q", e.Error(), "bad config")
	}
	if !Is(e, ErrCodeConfig) {
		t.Error("Is(ErrCodeConfig) = false, want true")
	}
	if Is(e, ErrCodeAuth) {
		t.Error("Is(ErrCodeAuth) = true, want false")
	}
}

func TestOError_Wrap(t *testing.T) {
	cause := errors.New("root cause")
	e := ErrAuth("auth failed", cause)
	if !errors.Is(e, cause) {
		t.Error("errors.Is(e, cause) = false, want true")
	}
	if !Is(e, ErrCodeAuth) {
		t.Error("Is(ErrCodeAuth) on wrapped = false, want true")
	}
	var o *OError
	if !errors.As(e, &o) {
		t.Fatal("errors.As to *OError failed")
	}
	if o.Err != cause {
		t.Error("o.Err != cause")
	}
}

func TestOError_NilCause(t *testing.T) {
	e := ErrProvider("provider not found")
	var o *OError
	if !errors.As(e, &o) {
		t.Fatal("errors.As to *OError failed")
	}
	if o.Err != nil {
		t.Errorf("Err = %v, want nil", o.Err)
	}
	if !Is(e, ErrCodeProvider) {
		t.Error("Is(ErrCodeProvider) = false, want true")
	}
}

func TestIs_Nil(t *testing.T) {
	if Is(nil, ErrCodeConfig) {
		t.Error("Is(nil, ...) = true, want false")
	}
}

func TestIs_NonOError(t *testing.T) {
	e := errors.New("plain error")
	if Is(e, ErrCodeConfig) {
		t.Error("Is(plain error, ...) = true, want false")
	}
}

func TestCode(t *testing.T) {
	if code, ok := Code(ErrBudget("over")); !ok || code != ErrCodeBudget {
		t.Errorf("Code(ErrBudget) = (%q, %v), want (BUDGET, true)", code, ok)
	}

	// Codes survive wrapping, so callers can inspect a returned error chain.
	wrapped := fmt.Errorf("run failed: %w", ErrAuth("no key"))
	if code, ok := Code(wrapped); !ok || code != ErrCodeAuth {
		t.Errorf("Code(wrapped ErrAuth) = (%q, %v), want (AUTH, true)", code, ok)
	}

	if _, ok := Code(errors.New("plain")); ok {
		t.Error("Code(plain error) ok = true, want false")
	}
	if _, ok := Code(nil); ok {
		t.Error("Code(nil) ok = true, want false")
	}
}

func TestConstructor_Names(t *testing.T) {
	tests := []struct {
		fn   func(string, ...error) error
		code ErrorCode
	}{
		{ErrConfig, ErrCodeConfig},
		{ErrAuth, ErrCodeAuth},
		{ErrProvider, ErrCodeProvider},
		{ErrBudget, ErrCodeBudget},
		{ErrNetwork, ErrCodeNetwork},
		{ErrPermission, ErrCodePermission},
		{ErrSystem, ErrCodeSystem},
	}
	for _, tt := range tests {
		e := tt.fn("test")
		if !Is(e, tt.code) {
			t.Errorf("%T: Is = false, want true", tt.fn)
		}
	}
}
