package tui

import "hjh/internal/contracts"

func failureReason(diagnostic *contracts.FailureDiagnostic) string {
	if diagnostic == nil {
		return ""
	}
	switch diagnostic.Code {
	case contracts.FailureResponse:
		return "provider response invalid"
	case contracts.FailureTransport:
		return "provider connection failed"
	case contracts.FailureProviderTimeout:
		return "provider deadline exceeded"
	case contracts.FailureHTTP:
		return "provider request failed"
	case contracts.FailureMissingCredential:
		return "credential unavailable"
	case contracts.FailureContextBudget:
		return "context budget exceeded"
	case contracts.FailureCommit:
		return "session save failed"
	case contracts.FailureCancelled:
		return "cancelled"
	case contracts.FailureStepLimit:
		return "step limit reached"
	default:
		return ""
	}
}

func failureNotice(diagnostic *contracts.FailureDiagnostic, outcome, detail string, recallAvailable bool) string {
	reason := failureReason(diagnostic)
	if reason == "" {
		return detail
	}
	prefix := "FAILED"
	if outcome == "cancelled" || diagnostic.Code == contracts.FailureCancelled {
		prefix = "CANCELLED"
	} else if outcome == "interrupted" {
		prefix = "INTERRUPTED"
	}
	text := prefix + " · " + reason
	if prefix == "FAILED" && recallAvailable {
		switch diagnostic.Code {
		case contracts.FailureResponse, contracts.FailureTransport, contracts.FailureProviderTimeout:
			text += " · try /recall"
		case contracts.FailureHTTP:
			if diagnostic.RetryAttempt != 0 {
				text += " · try /recall"
			}
		}
	}
	return text
}
