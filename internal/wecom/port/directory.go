package port

import "context"

var ErrDirectoryDisabled = directoryError("wecom directory provider disabled")

type directoryError string

func (err directoryError) Error() string { return string(err) }

// DirectoryFailure exposes a stable, non-PII classification for a failed
// read from the WeCom customer directory. Callers must not infer a provider
// write from this interface: DirectoryProvider remains read-only.
type DirectoryFailure interface {
	error
	DirectoryFailureCode() string
	DirectoryFailureRetryable() bool
}

// DirectoryFailureAttemptLimit optionally narrows River's normal attempt
// budget for one specific Provider classification. Zero keeps the job's
// configured limit. Callers must not infer a limit from the failure code.
type DirectoryFailureAttemptLimit interface {
	DirectoryFailure
	DirectoryFailureMaxAttempts() int
}

type ExternalContact struct {
	ExternalUserID string
	Name           string
	AvatarURL      string
	Gender         int16
	Type           int16
	CorpName       string
	UnionID        string
	FollowInfo     []ExternalContactFollowInfo
}

type ExternalContactFollowInfo struct {
	EmployeeID string
	Tags       []ExternalContactTag
}

type ExternalContactTag struct {
	ProviderTagID string
	Name          string
	Type          int16
}

type ExternalContactPage struct {
	Contacts   []ExternalContact
	NextCursor string
}

// DirectoryProvider is read-only. It has no method capable of changing a
// WeCom contact, remark, tag or ownership relationship.
type DirectoryProvider interface {
	DirectoryReady() bool
	ListContactStaff(context.Context) ([]string, error)
	BatchExternalContacts(context.Context, string, string, int) (ExternalContactPage, error)
}
