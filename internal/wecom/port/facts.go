// Package port contains verified WeCom protocol facts that may cross domain boundaries.
package port

import "strings"

// VerifiedExternalContact is produced only after a trusted WeCom/JSSDK or
// callback adapter has verified the provider context. HTTP request bodies may
// not construct this value directly.
type VerifiedExternalContact struct {
	CorpID         string
	ExternalUserID string
	Source         string
}

func (fact VerifiedExternalContact) Valid() bool {
	return strings.TrimSpace(fact.CorpID) == fact.CorpID && fact.CorpID != "" &&
		strings.TrimSpace(fact.ExternalUserID) == fact.ExternalUserID && fact.ExternalUserID != "" &&
		strings.TrimSpace(fact.Source) == fact.Source && fact.Source != ""
}
