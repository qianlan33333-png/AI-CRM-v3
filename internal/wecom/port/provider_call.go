package port

import "errors"

// ProviderWriteError records only whether the business Provider endpoint may
// have received the request. It never carries credentials, payloads or IDs.
type ProviderWriteError struct {
	Err       error
	Attempted bool
}

func (err *ProviderWriteError) Error() string { return err.Err.Error() }
func (err *ProviderWriteError) Unwrap() error { return err.Err }

func WrapProviderWriteError(err error, attempted bool) error {
	if err == nil {
		return nil
	}
	return &ProviderWriteError{Err: err, Attempted: attempted}
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
