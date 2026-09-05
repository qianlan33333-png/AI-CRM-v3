package main

import (
	"context"
	"errors"
	"strings"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	accessport "github.com/qianlan33333-png/AI-CRM-v3/internal/access/port"
	aiassistantapp "github.com/qianlan33333-png/AI-CRM-v3/internal/aiassistant/app"
	aiassistantport "github.com/qianlan33333-png/AI-CRM-v3/internal/aiassistant/port"
	customerapp "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/app"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	mediaport "github.com/qianlan33333-png/AI-CRM-v3/internal/media/port"
	mediastore "github.com/qianlan33333-png/AI-CRM-v3/internal/media/store"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/outbound"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
)

// Concrete adapters below intentionally live in the composition root: AI
// Assistant depends on stable values and never imports Customer, Access, or
// Media stores.
type aiCustomerSnapshotAdapter struct {
	read func(context.Context, customerdomain.CustomerID) (customerdomain.CustomerID, customerdomain.Status, string, string, error)
}

func (a aiCustomerSnapshotAdapter) CustomerSnapshot(ctx context.Context, id customerdomain.CustomerID) (aiassistantapp.CustomerSnapshot, error) {
	canonical, status, name, label, err := a.read(ctx, id)
	if errors.Is(err, customerapp.ErrNotFound) {
		// A missing directory projection is a target eligibility fact, not a
		// transient database failure. The service records it safely and creates
		// no recipient for this target.
		return aiassistantapp.CustomerSnapshot{}, nil
	}
	return aiassistantapp.CustomerSnapshot{CanonicalID: canonical, Status: status, DisplayName: name, OneIDLabel: label}, err
}

type aiStaffSnapshotAdapter struct{ repository accessport.Repository }

func (a aiStaffSnapshotAdapter) StaffSnapshot(ctx context.Context, id int64) (aiassistantapp.StaffSnapshot, error) {
	user, err := a.repository.UserByID(ctx, id, false)
	if errors.Is(err, accessdomain.ErrNotFound) {
		return aiassistantapp.StaffSnapshot{}, nil
	}
	if err != nil {
		return aiassistantapp.StaffSnapshot{}, err
	}
	return aiassistantapp.StaffSnapshot{ID: user.ID, DisplayName: user.DisplayName, Active: user.Active}, nil
}

func (a aiStaffSnapshotAdapter) StaffByWeComUserID(ctx context.Context, value string) (aiassistantapp.StaffSnapshot, error) {
	user, err := a.repository.UserByWeComUserID(ctx, value, false)
	if errors.Is(err, accessdomain.ErrNotFound) {
		return aiassistantapp.StaffSnapshot{}, nil
	}
	if err != nil {
		return aiassistantapp.StaffSnapshot{}, err
	}
	return aiassistantapp.StaffSnapshot{ID: user.ID, DisplayName: user.DisplayName, Active: user.Active}, nil
}

type aiMaterialAdapter struct {
	capturer   mediaport.GroupOpsMaterialSourceCapturer
	references mediaport.MaterialReferenceRegistrar
	legacy     mediaport.LegacyMaterialMappingResolver
}

func (a aiMaterialAdapter) ResolveMaterial(ctx context.Context, block aiassistantport.ContentBlock) (aiassistantport.ContentBlock, error) {
	kind := block.MaterialKind
	if block.LegacySourceSystem != "" || block.LegacyMaterialID != "" {
		if a.legacy == nil || block.LegacySourceSystem == "" || block.LegacyMaterialID == "" {
			return aiassistantport.ContentBlock{}, aiassistantapp.ErrLegacyMaterialUnmapped
		}
		mapping, found, err := a.legacy.ResolveLegacyMaterialMapping(ctx, mediaport.LegacyMaterialReference{SourceSystem: block.LegacySourceSystem, MaterialKind: kind, LegacyID: block.LegacyMaterialID})
		if err != nil {
			return aiassistantport.ContentBlock{}, err
		}
		if !found || mapping.MaterialKind != kind || mapping.MaterialID < 1 || mapping.SourceDigest == "" {
			return aiassistantport.ContentBlock{}, aiassistantapp.ErrLegacyMaterialUnmapped
		}
		block.MaterialID = mapping.MaterialID
		block.LegacySourceSystem, block.LegacyMaterialID = "", ""
		// A mapping is only usable if the current Media fact still has exactly
		// the digest verified by the frozen-source import.
		snapshot, captureErr := a.capture(ctx, kind, block.MaterialID)
		if errors.Is(captureErr, mediastore.ErrNotFound) || errors.Is(captureErr, mediastore.ErrConflict) || len(snapshot.References) != 1 || snapshot.References[0].SourceDigest != mapping.SourceDigest {
			return aiassistantport.ContentBlock{}, aiassistantapp.ErrLegacyMaterialUnmapped
		}
		if captureErr != nil {
			return aiassistantport.ContentBlock{}, captureErr
		}
		block.MaterialDigest = effectport.Digest(mapping.SourceDigest)
		return block, nil
	}
	snapshot, err := a.capture(ctx, kind, block.MaterialID)
	if err != nil || len(snapshot.References) != 1 {
		return aiassistantport.ContentBlock{}, errors.New("material unavailable")
	}
	block.MaterialKind = kind
	block.MaterialDigest = effectport.Digest(snapshot.References[0].SourceDigest)
	return block, nil
}

func (a aiMaterialAdapter) capture(ctx context.Context, kind string, id int64) (mediaport.GroupOpsMaterialSourceSnapshot, error) {
	if a.capturer == nil || id < 1 || kind == "" {
		return mediaport.GroupOpsMaterialSourceSnapshot{}, errors.New("material unavailable")
	}
	return a.capturer.CaptureGroupOpsMaterialSources(ctx, mediaport.GroupOpsMaterialPlan{References: []mediaport.GroupOpsMaterialReference{{Kind: kind, ID: id}}})
}

func (a aiMaterialAdapter) RegisterMaterialReference(ctx context.Context, block aiassistantport.ContentBlock, reference effectport.Digest) error {
	if a.references == nil || !effectport.ValidDigest(reference) {
		return errors.New("material reference unavailable")
	}
	return a.references.RegisterMediaReference(ctx, mediaport.MaterialReference{MaterialKind: block.MaterialKind, MaterialID: block.MaterialID, Owner: "aiassistant.content-version", ReferenceDigest: string(reference)})
}

type aiFollowReader interface {
	IsActive(context.Context, string, string, customerdomain.CustomerID) (bool, error)
}
type aiPrivateTargetResolver struct {
	uow           platformport.UnitOfWork
	identities    identityport.OutboundWeComIdentityReader
	access        accessport.Repository
	relationships aiFollowReader
	corpID        string
}

func (a aiPrivateTargetResolver) ResolvePrivateMessageTarget(ctx context.Context, customerID customerdomain.CustomerID, staffID int64) (outbound.PrivateMessageTarget, error) {
	var target outbound.PrivateMessageTarget
	err := a.uow.Within(ctx, func(tx context.Context) error {
		user, err := a.access.UserByID(tx, staffID, false)
		if err != nil || !user.Active || user.WeComUserID == "" {
			return errors.New("staff channel identity unavailable")
		}
		external, found, err := a.identities.VerifiedWeComIdentityForCustomer(tx, customerID, a.corpID)
		if err != nil || !found {
			return errors.New("customer channel identity unavailable")
		}
		active, err := a.relationships.IsActive(tx, a.corpID, user.WeComUserID, customerID)
		if err != nil || !active {
			return errors.New("customer relationship unavailable")
		}
		target = outbound.PrivateMessageTarget{ExternalUserID: external, StaffUserID: user.WeComUserID}
		return nil
	})
	return target, err
}

type aiImageReader interface {
	GetImageVariant(context.Context, int64, string) (mediaport.ImageVariant, error)
}
type aiMaterialDetails interface {
	MiniProgram(context.Context, int64) (map[string]any, error)
	GroupInvite(context.Context, int64) (map[string]any, error)
}
type aiAttachmentReader interface {
	Attachment(context.Context, int64) (map[string]any, []byte, error)
}
type aiPrivatePayloadReader struct {
	content     aiassistantport.OutboundPayloadReader
	images      aiImageReader
	materials   aiMaterialDetails
	attachments aiAttachmentReader
}

func (a aiPrivatePayloadReader) LoadPrivateMessagePayload(ctx context.Context, reference string, digest effectport.Digest) (outbound.PrivateMessagePayload, error) {
	content, err := a.content.LoadOutboundContent(ctx, reference, digest)
	if err != nil {
		return outbound.PrivateMessagePayload{}, err
	}
	result := outbound.PrivateMessagePayload{}
	for _, block := range content.Blocks {
		switch block.Kind {
		case aiassistantport.ContentText:
			if result.Text != "" {
				result.Text += "\n"
			}
			result.Text += block.Text
		case aiassistantport.ContentImage:
			variant, e := a.images.GetImageVariant(ctx, block.MaterialID, "original")
			if e != nil {
				return outbound.PrivateMessagePayload{}, e
			}
			result.Attachments = append(result.Attachments, outbound.PrivateMessageAttachment{Kind: "image", Content: variant.Content, FileName: imageFileName(variant.MediaType), MediaType: variant.MediaType})
		case aiassistantport.ContentMiniProgram:
			item, e := a.materials.MiniProgram(ctx, block.MaterialID)
			if e != nil {
				return outbound.PrivateMessagePayload{}, e
			}
			thumb, ok := number(item["thumb_image_id"])
			if !ok {
				return outbound.PrivateMessagePayload{}, errors.New("mini program thumbnail unavailable")
			}
			variant, e := a.images.GetImageVariant(ctx, thumb, "original")
			if e != nil {
				return outbound.PrivateMessagePayload{}, e
			}
			result.Attachments = append(result.Attachments, outbound.PrivateMessageAttachment{Kind: "mini_program", Content: variant.Content, FileName: imageFileName(variant.MediaType), MediaType: variant.MediaType, AppID: text(item["appid"]), PagePath: text(item["pagepath"]), Title: text(item["title"])})
		case aiassistantport.ContentLink:
			item, e := a.materials.GroupInvite(ctx, block.MaterialID)
			if e != nil {
				return outbound.PrivateMessagePayload{}, e
			}
			result.Attachments = append(result.Attachments, outbound.PrivateMessageAttachment{Kind: "link", Title: text(item["title"]), Description: text(item["description"]), URL: text(item["join_url"])})
		case aiassistantport.ContentAttachment:
			if a.attachments == nil {
				return outbound.PrivateMessagePayload{}, errors.New("attachment reader unavailable")
			}
			metadata, content, e := a.attachments.Attachment(ctx, block.MaterialID)
			if e != nil || !enabled(metadata) || text(metadata["mime_type"]) != "application/pdf" || len(content) == 0 {
				return outbound.PrivateMessagePayload{}, errors.New("attachment unavailable")
			}
			result.Attachments = append(result.Attachments, outbound.PrivateMessageAttachment{Kind: "file", Content: content, FileName: text(metadata["file_name"]), MediaType: text(metadata["mime_type"])})
		default:
			return outbound.PrivateMessagePayload{}, errors.New("unsupported content kind")
		}
	}
	result.Text = strings.TrimSpace(result.Text)
	return result, nil
}
func text(value any) string             { out, _ := value.(string); return out }
func enabled(value map[string]any) bool { out, _ := value["enabled"].(bool); return out }
func imageFileName(mediaType string) string {
	if mediaType == "image/png" {
		return "image.png"
	}
	return "image.jpg"
}
func number(value any) (int64, bool) {
	switch v := value.(type) {
	case int64:
		return v, v > 0
	case int:
		return int64(v), v > 0
	case float64:
		return int64(v), v > 0 && v == float64(int64(v))
	default:
		return 0, false
	}
}
