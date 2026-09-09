package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"strconv"
	"time"

	mediaport "github.com/qianlan33333-png/AI-CRM-v3/internal/media/port"
	outboundport "github.com/qianlan33333-png/AI-CRM-v3/internal/outbound/port"
)

// sidebarImagePreparation freezes an enabled Media-owned variant before
// submitting the upload intent to Outbound. It never calls a Provider and
// never uses the local image ID as a WeCom media ID.
type sidebarImagePreparation struct {
	images   mediaport.EnabledImageVariantReader
	preparer outboundport.SidebarImagePreparer
	scope    string
	enabled  bool
}

func (a sidebarImagePreparation) ReadSidebarImageForSend(ctx context.Context, id int64, through time.Time) (mediaport.SidebarImageSendMaterial, error) {
	if !a.enabled || a.images == nil || a.preparer == nil {
		return mediaport.SidebarImageSendMaterial{}, mediaport.ErrSidebarMaterialNotReady
	}
	// The existing 1080-pixel variant produces a bounded JPEG/PNG from local
	// trusted bytes; it avoids uploading arbitrary URLs or unbounded originals.
	image, err := a.images.GetEnabledImageVariant(ctx, id, "mobile_1080")
	if err != nil {
		return mediaport.SidebarImageSendMaterial{}, err
	}
	if len(image.Content) <= 5 || len(image.Content) > 2<<20 || (image.MediaType != "image/jpeg" && image.MediaType != "image/png") {
		return mediaport.SidebarImageSendMaterial{}, errors.New("image cannot be prepared for sidebar")
	}
	ext := ".jpg"
	if image.MediaType == "image/png" {
		ext = ".png"
	}
	prepared, err := a.preparer.PrepareSidebarImage(ctx, outboundport.SidebarImagePreparationSource{
		ImageID: id, Scope: a.scope, SourceDigest: sha256.Sum256(image.Content), Content: image.Content,
		FileName: "sidebar-image-" + strconv.FormatInt(id, 10) + ext, MediaType: image.MediaType,
	}, through)
	if err != nil {
		return mediaport.SidebarImageSendMaterial{}, mediaport.ErrSidebarMaterialNotReady
	}
	switch prepared.State {
	case "ready":
		if prepared.MediaID == "" || !prepared.ReadyUntil.After(through) {
			return mediaport.SidebarImageSendMaterial{}, mediaport.ErrSidebarMaterialNotReady
		}
		return mediaport.SidebarImageSendMaterial{ImageID: id, MediaID: prepared.MediaID, ReadyUntil: prepared.ReadyUntil}, nil
	case "queued", "accepted", "attempted", "retryable_failed":
		return mediaport.SidebarImageSendMaterial{}, mediaport.ErrSidebarMaterialPreparing
	case "outcome_unknown":
		return mediaport.SidebarImageSendMaterial{}, mediaport.ErrSidebarMaterialOutcomeUnknown
	default:
		return mediaport.SidebarImageSendMaterial{}, mediaport.ErrSidebarMaterialPreparationFailed
	}
}
