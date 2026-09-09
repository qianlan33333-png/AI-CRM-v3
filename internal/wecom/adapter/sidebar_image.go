package adapter

import (
	"context"
	"strings"
	"time"

	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	outboundport "github.com/qianlan33333-png/AI-CRM-v3/internal/outbound/port"
)

// UploadSidebarImage is a transport leaf invoked only by Outbound's leased
// media-preparation Provider. It uploads bytes and never sends a message.
func (c *Client) UploadSidebarImage(ctx context.Context, source outboundport.SidebarImagePreparationSource) (outboundport.SidebarImageUploadReceipt, bool, error) {
	if c == nil || !c.config.Enabled || source.Scope != string(effectport.Hash("sidebar.image.scope.config.v1", c.config.CorpID+":"+c.config.AgentID)) || len(source.Content) <= 5 || len(source.Content) > 2<<20 || (source.MediaType != "image/jpeg" && source.MediaType != "image/png") || strings.TrimSpace(source.FileName) == "" || strings.ContainsAny(source.FileName, "\r\n\x00") {
		return outboundport.SidebarImageUploadReceipt{}, false, ErrUnavailable
	}
	// Use the same application credential that signs the sidebar's JSSDK.
	token, err := c.accessToken(ctx)
	if err != nil {
		return outboundport.SidebarImageUploadReceipt{}, false, err
	}
	started := c.now()
	mediaID, err := c.uploadPrivateImage(ctx, token, source.FileName, source.MediaType, source.Content)
	if err != nil {
		return outboundport.SidebarImageUploadReceipt{}, true, err
	}
	// WeCom temporary media lasts three days. Reserve two hours for clock and
	// queue margins; callers also demand validity through the SDK grant expiry.
	return outboundport.SidebarImageUploadReceipt{MediaID: mediaID, ReadyUntil: started.Add(70 * time.Hour)}, true, nil
}
