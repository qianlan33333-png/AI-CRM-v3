package adapter

import (
	"context"
	"encoding/json"
	w "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
	"net/http"
	"net/url"
)

// Only outbound receives this writer. Always bind one existing group and
// explicitly disable automatic group creation.
func (c *Client) CreateInvitationCode(ctx context.Context, chat string) (w.InvitationCode, error) {
	if !c.DirectoryReady() || invalid(chat) {
		return w.InvitationCode{}, w.WrapProviderWriteError(ErrResponse, false)
	}
	token, err := c.contactAccessToken(ctx)
	if err != nil {
		return w.InvitationCode{}, w.WrapProviderWriteError(err, false)
	}
	body, _ := json.Marshal(map[string]any{"scene": 2, "auto_create_room": 0, "chat_id_list": []string{chat}})
	result, err := c.requestJSON(ctx, http.MethodPost, "/cgi-bin/externalcontact/groupchat/add_join_way", url.Values{"access_token": {token}}, body)
	if err != nil {
		return w.InvitationCode{}, w.WrapProviderWriteError(err, true)
	}
	if invalid(result.ConfigID) {
		return w.InvitationCode{}, w.WrapProviderWriteError(ErrResponse, true)
	}
	body, _ = json.Marshal(map[string]string{"config_id": result.ConfigID})
	detail, err := c.requestJSON(ctx, http.MethodPost, "/cgi-bin/externalcontact/groupchat/get_join_way", url.Values{"access_token": {token}}, body)
	if err != nil {
		return w.InvitationCode{ConfigID: result.ConfigID}, w.WrapProviderWriteError(err, true)
	}
	if !validProviderHTTPS(detail.JoinWay.QRCode) {
		return w.InvitationCode{ConfigID: result.ConfigID}, w.WrapProviderWriteError(ErrResponse, true)
	}
	return w.InvitationCode{ConfigID: result.ConfigID, QRCode: detail.JoinWay.QRCode}, nil
}
