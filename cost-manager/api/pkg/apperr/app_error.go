package apperr

import (
	"errors"
	"strings"
)

type AppError struct {
	Kind    error
	Outcome string
	Cause   error
}

func (e *AppError) Error() string {
	if e == nil || e.Kind == nil {
		return "unknown"
	}
	return e.Kind.Error()
}

func (e *AppError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Kind
}

func Wrap(kind error, cause error, outcome ...string) error {
	app := &AppError{Kind: kind, Cause: cause}
	if len(outcome) > 0 {
		app.Outcome = strings.TrimSpace(outcome[0])
	}
	return app
}

func As(err error) (*AppError, bool) {
	if err == nil {
		return nil, false
	}
	var appErr *AppError
	if !errors.As(err, &appErr) {
		return nil, false
	}
	return appErr, true
}
