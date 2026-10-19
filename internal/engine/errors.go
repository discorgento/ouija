package engine

// ErrorCode categorizes failures so callers (CLI, MCP, tests) can react differently.
type ErrorCode string

const (
	ErrCodeConfig     ErrorCode = "CONFIG"     // invalid config (missing field, bad value)
	ErrCodeAuth       ErrorCode = "AUTH"       // API key missing/invalid
	ErrCodeProvider   ErrorCode = "PROVIDER"   // provider not registered / init failed
	ErrCodeBudget     ErrorCode = "BUDGET"     // token/cost budget exceeded
	ErrCodeNetwork    ErrorCode = "NETWORK"    // transport error (timeout, DNS, 5xx)
	ErrCodePermission ErrorCode = "PERMISSION" // tool permission denied
	ErrCodeSystem     ErrorCode = "SYSTEM"     // bug / unexpected
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

// Is checks if the error (or its chain) has the given code.
func Is(err error, code ErrorCode) bool {
	if err == nil {
		return false
	}
	for err != nil {
		if e, ok := err.(*OError); ok && e.Code == code {
			return true
		}
		err = unwrap(err)
	}
	return false
}

// Code returns the ErrorCode carried by err (or the nearest wrapper in its
// chain). ok is false when no code is present, so callers can fall back to a
// generic message.
func Code(err error) (code ErrorCode, ok bool) {
	for err != nil {
		if e, ok := err.(*OError); ok {
			return e.Code, true
		}
		err = unwrap(err)
	}
	return "", false
}

// Helper constructors.
func ErrConfig(msg string, cause ...error) error {
	e := &OError{Code: ErrCodeConfig, Message: msg}
	if len(cause) > 0 {
		e.Err = cause[0]
	}
	return e
}
func ErrAuth(msg string, cause ...error) error {
	e := &OError{Code: ErrCodeAuth, Message: msg}
	if len(cause) > 0 {
		e.Err = cause[0]
	}
	return e
}
func ErrProvider(msg string, cause ...error) error {
	e := &OError{Code: ErrCodeProvider, Message: msg}
	if len(cause) > 0 {
		e.Err = cause[0]
	}
	return e
}
func ErrBudget(msg string, cause ...error) error {
	e := &OError{Code: ErrCodeBudget, Message: msg}
	if len(cause) > 0 {
		e.Err = cause[0]
	}
	return e
}
func ErrNetwork(msg string, cause ...error) error {
	e := &OError{Code: ErrCodeNetwork, Message: msg}
	if len(cause) > 0 {
		e.Err = cause[0]
	}
	return e
}
func ErrPermission(msg string, cause ...error) error {
	e := &OError{Code: ErrCodePermission, Message: msg}
	if len(cause) > 0 {
		e.Err = cause[0]
	}
	return e
}
func ErrSystem(msg string, cause ...error) error {
	e := &OError{Code: ErrCodeSystem, Message: msg}
	if len(cause) > 0 {
		e.Err = cause[0]
	}
	return e
}

// unwrap follows error chains (errors.Unwrap / custom Unwrap).
func unwrap(err error) error {
	type unwrapper interface{ Unwrap() error }
	if u, ok := err.(unwrapper); ok {
		return u.Unwrap()
	}
	return nil
}
