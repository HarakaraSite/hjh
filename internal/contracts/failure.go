package contracts

import (
	"context"
	"errors"
)

const (
	FailureResponse          = "response_error"
	FailureTransport         = "transport_error"
	FailureProviderTimeout   = "provider_timeout"
	FailureHTTP              = "http_error"
	FailureMissingCredential = "missing_credential"
	FailureContextBudget     = "context_budget_exceeded"
	FailureCommit            = "commit_error"
	FailureCancelled         = "turn_cancelled"
	FailureStepLimit         = "model_step_limit"
)

// FailureDiagnostic is the small, stable classification retained alongside an error.
// Provider-specific details remain in the error text or its underlying cause.
type FailureDiagnostic struct {
	Code        string `json:"code"`
	ParseReason string `json:"parseReason,omitempty"`
	Field       string `json:"field,omitempty"`
	HTTPStatus  int    `json:"httpStatus,omitempty"`
}

// FailureError attaches a classification while preserving the original error chain.
type FailureError struct {
	Err        error
	Diagnostic FailureDiagnostic
}

func (failure *FailureError) Error() string {
	if failure == nil || failure.Err == nil {
		return ""
	}
	return failure.Err.Error()
}

func (failure *FailureError) Unwrap() error {
	if failure == nil {
		return nil
	}
	return failure.Err
}

// WithFailure attaches diagnostic to err. A nil error remains nil.
func WithFailure(err error, diagnostic FailureDiagnostic) error {
	if err == nil {
		return nil
	}
	return &FailureError{Err: err, Diagnostic: diagnostic}
}

// FailureFor finds the applicable diagnostic, preferring execution commit failures
// and cancellation because those are owned by the execution lifecycle.
func FailureFor(err error) *FailureDiagnostic {
	if err == nil {
		return nil
	}
	var executionError *ExecutionError
	if errors.As(err, &executionError) {
		return &FailureDiagnostic{Code: FailureCommit}
	}
	if errors.Is(err, context.Canceled) {
		return &FailureDiagnostic{Code: FailureCancelled}
	}
	var failure *FailureError
	if errors.As(err, &failure) {
		return CloneFailure(&failure.Diagnostic)
	}
	return nil
}

// CloneFailure copies the scalar diagnostic so callers can safely retain it.
func CloneFailure(diagnostic *FailureDiagnostic) *FailureDiagnostic {
	if diagnostic == nil {
		return nil
	}
	copy := *diagnostic
	return &copy
}
