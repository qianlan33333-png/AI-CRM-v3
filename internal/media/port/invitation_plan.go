package port

import (
	"context"
	"errors"
	g "github.com/qianlan33333-png/AI-CRM-v3/internal/groupops/port"
	"time"
)

var ErrInvitationVersion = errors.New("invitation plan version changed")

type InvitationBinding struct {
	ChatID    string `json:"chat_id"`
	Retired   bool   `json:"retired"`
	QRCode    string `json:"qr_code,omitempty"`
	CodeState string `json:"code_state"`
}
type InvitationPlan struct {
	ID            int64               `json:"id"`
	Name          string              `json:"name"`
	Title         string              `json:"title"`
	Description   string              `json:"description"`
	CoverImageID  int64               `json:"cover_image_id"`
	Mode          string              `json:"mode"`
	Threshold     *int                `json:"threshold"`
	Enabled       bool                `json:"enabled"`
	Version       int64               `json:"version"`
	Token         string              `json:"token"`
	JoinURL       string              `json:"join_url"`
	State         string              `json:"state"`
	CurrentChatID string              `json:"current_chat_id"`
	Bindings      []InvitationBinding `json:"bindings"`
}
type InvitationInput struct {
	Observations map[string]g.CatalogGroup `json:"-"`
	ID           int64                     `json:"id"`
	Version      int64                     `json:"version"`
	Name         string                    `json:"name"`
	Title        string                    `json:"title"`
	Description  string                    `json:"description"`
	CoverImageID int64                     `json:"cover_image_id"`
	Mode         string                    `json:"mode"`
	Threshold    *int                      `json:"threshold"`
	Enabled      bool                      `json:"enabled"`
	ChatIDs      []string                  `json:"chat_ids"`
}
type InvitationSwitch struct {
	From string    `json:"from"`
	To   string    `json:"to"`
	At   time.Time `json:"at"`
}
type InvitationCodeIntent struct {
	ChatID       string
	SourceDigest string
	EffectID     string
}
type InvitationCodeCompletion struct {
	EffectID string
	State    string
	ConfigID string
	QRCode   string
}
type InvitationCodeStore interface {
	ReadInvitationCodeIntent(context.Context, string) (InvitationCodeIntent, error)
	CompleteInvitationCode(context.Context, InvitationCodeCompletion) error
}
