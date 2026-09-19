package port

import "context"

type InvitationCode struct {
	ConfigID string `json:"config_id"`
	QRCode   string `json:"qr_code"`
}
type InvitationCodeProvider interface {
	CreateInvitationCode(context.Context, string) (InvitationCode, error)
}
