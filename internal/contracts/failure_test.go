package contracts

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func TestFailureWrapperPreservesCauseAndDiagnostic(t *testing.T) {
	cause := errors.New("provider body could not be decoded")
	diagnostic := FailureDiagnostic{Code: FailureResponse, ParseReason: "invalid_tool_arguments", Field: "choices[0].delta.tool_calls[0].function.arguments"}
	wrapped := WithFailure(cause, diagnostic)

	if wrapped == nil || wrapped.Error() != cause.Error() || !errors.Is(wrapped, cause) {
		t.Fatalf("wrapped error = %v; want cause text and errors.Is", wrapped)
	}
	var found *FailureError
	if !errors.As(wrapped, &found) || found.Diagnostic != diagnostic {
		t.Fatalf("FailureError = %#v; want %#v", found, diagnostic)
	}
	got := FailureFor(wrapped)
	if got == nil || *got != diagnostic {
		t.Fatalf("FailureFor = %#v; want %#v", got, diagnostic)
	}
	got.Code = FailureHTTP
	if found.Diagnostic.Code != FailureResponse {
		t.Fatalf("FailureFor returned shared diagnostic storage: %#v", found.Diagnostic)
	}
	if WithFailure(nil, diagnostic) != nil {
		t.Fatal("WithFailure(nil) must return nil")
	}
}

func TestFailureForPrefersExecutionCommitAndCancellation(t *testing.T) {
	for _, testCase := range []struct {
		name string
		err  error
		code string
	}{
		{
			name: "commit marker",
			err:  &ExecutionError{Err: WithFailure(context.Canceled, FailureDiagnostic{Code: FailureHTTP})},
			code: FailureCommit,
		},
		{
			name: "cancellation",
			err:  WithFailure(context.Canceled, FailureDiagnostic{Code: FailureProviderTimeout}),
			code: FailureCancelled,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			got := FailureFor(testCase.err)
			if got == nil || got.Code != testCase.code {
				t.Fatalf("FailureFor = %#v; want code %q", got, testCase.code)
			}
		})
	}
	if FailureFor(errors.New("unclassified")) != nil {
		t.Fatal("unclassified error received a diagnostic")
	}
}

func TestFailureDiagnosticJSONOmitsEmptyScalars(t *testing.T) {
	encoded, err := json.Marshal(FailureDiagnostic{Code: FailureHTTP, HTTPStatus: 429})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(encoded), `{"code":"http_error","httpStatus":429}`; got != want {
		t.Fatalf("diagnostic JSON = %s; want %s", got, want)
	}
}

func TestCloneFailureHandlesNilAndCopiesValue(t *testing.T) {
	if CloneFailure(nil) != nil {
		t.Fatal("CloneFailure(nil) must return nil")
	}
	original := &FailureDiagnostic{Code: FailureHTTP, HTTPStatus: 503}
	copy := CloneFailure(original)
	if copy == nil || copy == original || *copy != *original {
		t.Fatalf("CloneFailure = %#v; original = %#v", copy, original)
	}
}
