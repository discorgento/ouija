package engine

import "errors"

// ErrorCode categorizes failures so callers (CLI, MCP, tests) can react differently.
type ErrorCode string

const (
	ErrCodeConfig   ErrorCode = "CONFIG"   // invalid config (missing field, bad value)
	ErrCodeAuth     ErrorCode = "AUTH"     // API key missing/invalid
	ErrCodeProvider ErrorCode = "PROVIDER" // provider not registered / init failed
	ErrCodeBudget   ErrorCode = "BUDGET"   // token/cost budget exceeded
	ErrCodeNetwork  ErrorCode = "NETWORK"  // transport error (DNS, refused connection), already retried
	ErrCodeStream   ErrorCode = "STREAM"   // a streamed reply broke off or went silent mid-way: retryable
)

// OError wraps an error with a stable code.
type OError struct {
	Code    ErrorCode
	Message string
	Err     error // underlying cause (optional)
}

func (e *OError) Error() string {
	if e.Err != nil {
		return e.Message + ": " + e.Err.Error()
	}
	return e.Message
}

func (e *OError) Unwrap() error { return e.Err }

// Is reports whether err or any error it wraps (also through errors.Join and
// multi-%w wrapping) carries code.
func Is(err error, code ErrorCode) bool {
	switch e := err.(type) {
	case nil:
		return false
	case *OError:
		if e.Code == code {
			return true
		}
	}
	switch u := err.(type) {
	case interface{ Unwrap() error }:
		return Is(u.Unwrap(), code)
	case interface{ Unwrap() []error }:
		for _, inner := range u.Unwrap() {
			if Is(inner, code) {
				return true
			}
		}
	}
	return false
}

// Code returns the ErrorCode of the nearest *OError in err's chain. ok is
// false when none carries one, so callers can fall back to a generic message.
func Code(err error) (code ErrorCode, ok bool) {
	var e *OError
	if errors.As(err, &e) {
		return e.Code, true
	}
	return "", false
}

func newOError(code ErrorCode, msg string, cause []error) error {
	e := &OError{Code: code, Message: msg}
	if len(cause) > 0 {
		e.Err = cause[0]
	}
	return e
}

// Constructors, one per code; the optional cause is kept for errors.Is/As.
func ErrConfig(msg string, cause ...error) error   { return newOError(ErrCodeConfig, msg, cause) }
func ErrAuth(msg string, cause ...error) error     { return newOError(ErrCodeAuth, msg, cause) }
func ErrProvider(msg string, cause ...error) error { return newOError(ErrCodeProvider, msg, cause) }
func ErrBudget(msg string, cause ...error) error   { return newOError(ErrCodeBudget, msg, cause) }
func ErrNetwork(msg string, cause ...error) error  { return newOError(ErrCodeNetwork, msg, cause) }

// ErrStream is a streamed reply that started and then broke off or went
// silent: the one failure worth repeating the call for.
func ErrStream(msg string, cause ...error) error { return newOError(ErrCodeStream, msg, cause) }
