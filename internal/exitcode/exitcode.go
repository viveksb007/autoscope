// Package exitcode defines the stable exit-code contract from docs/TOOL.md.
package exitcode

import "errors"

const (
	OK       = 0
	User     = 1   // Bad flag, missing arg, agent has no metrics endpoint.
	Cluster  = 2   // Apiserver unreachable, RBAC denial, scheduling failure.
	Node     = 3   // nsenter/remote command failed, unit absent.
	Internal = 4   // Should never happen, file a bug.
	Sigint   = 130 // SIGINT.
)

// Error wraps an error with a stable exit code.
type Error struct {
	Code int
	Err  error
}

func (e *Error) Error() string { return e.Err.Error() }
func (e *Error) Unwrap() error { return e.Err }

// Wrap returns an Error tagging err with the given code. err MUST be non-nil.
func Wrap(code int, err error) error {
	if err == nil {
		return nil
	}
	return &Error{Code: code, Err: err}
}

// CodeOf returns the exit code embedded in err (walking the chain), or
// Internal if err is non-nil but uncoded, or OK if err is nil.
func CodeOf(err error) int {
	if err == nil {
		return OK
	}
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return Internal
}
