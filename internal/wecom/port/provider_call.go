package port

import "errors"

// ProviderWriteError records only whether the business Provider endpoint may
// have received the request and the safe transport disposition. It never
// carries credentials, payloads, response bodies, or external identifiers.
type ProviderWriteError struct {
	Err            error
	Attempted      bool
	OutcomeUnknown bool
	Retryable      bool
}

func (err *ProviderWriteError) Error() string { return err.Err.Error() }
func (err *ProviderWriteError) Unwrap() error { return err.Err }

// WrapProviderWriteError preserves the legacy conservative contract: any
// attempted but unclassified failure is outcome_unknown rather than retried.
func WrapProviderWriteError(err error, attempted bool) error {
	return WrapProviderWriteDisposition(err, attempted, attempted, false)
}

// WrapProviderWriteOutcome retains the Owner-transfer call shape while making
// the classified result available to every outbound leaf.
func WrapProviderWriteOutcome(err error, attempted, outcomeUnknown bool) error {
	return WrapProviderWriteDisposition(err, attempted, outcomeUnknown, false)
}

// WrapProviderWriteDisposition is for adapters that completed an HTTP exchange
// and can distinguish a definite rejection, a retry-safe pre-request failure,
// and an indeterminate post-request disconnect.
func WrapProviderWriteDisposition(err error, attempted, outcomeUnknown, retryable bool) error {
	if err == nil {
		return nil
	}
	if outcomeUnknown || attempted {
		retryable = false
	}
	return &ProviderWriteError{Err: err, Attempted: attempted, OutcomeUnknown: outcomeUnknown, Retryable: retryable}
}

func ProviderCallAttempted(err error) bool {
	var providerErr *ProviderWriteError
	if errors.As(err, &providerErr) {
		return providerErr.Attempted
	}
	// Unknown adapters are treated conservatively: an unclassified error may
	// have crossed the business write boundary and must not be blindly retried.
	return err != nil
}

func ProviderOutcomeUnknown(err error) bool {
	var providerErr *ProviderWriteError
	return errors.As(err, &providerErr) && providerErr.OutcomeUnknown
}

func ProviderRetryable(err error) bool {
	var providerErr *ProviderWriteError
	return errors.As(err, &providerErr) && providerErr.Retryable
}

func ProviderWriteClassified(err error) bool {
	var providerErr *ProviderWriteError
	return errors.As(err, &providerErr)
}
