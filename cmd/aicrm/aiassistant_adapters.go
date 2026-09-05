package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"strings"

	accessport "github.com/qianlan33333-png/AI-CRM-v3/internal/access/port"
	aiassistantapp "github.com/qianlan33333-png/AI-CRM-v3/internal/aiassistant/app"
	aiassistantport "github.com/qianlan33333-png/AI-CRM-v3/internal/aiassistant/port"
	automationport "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/port"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	mediaport "github.com/qianlan33333-png/AI-CRM-v3/internal/media/port"
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
	return aiassistantapp.CustomerSnapshot{CanonicalID: canonical, Status: status, DisplayName: name, OneIDLabel: label}, err
}

type aiStaffSnapshotAdapter struct{ repository accessport.Repository }

func (a aiStaffSnapshotAdapter) StaffSnapshot(ctx context.Context, id int64) (aiassistantapp.StaffSnapshot, error) {
	user, err := a.repository.UserByID(ctx, id, false)
	if err != nil {
		return aiassistantapp.StaffSnapshot{}, err
	}
	return aiassistantapp.StaffSnapshot{ID: user.ID, DisplayName: user.DisplayName, Active: user.Active}, nil
}

type aiMaterialAdapter struct {
	capturer   mediaport.GroupOpsMaterialSourceCapturer
	references mediaport.MaterialReferenceRegistrar
}

func (a aiMaterialAdapter) ResolveMaterial(ctx context.Context, block aiassistantport.ContentBlock) (aiassistantport.ContentBlock, error) {
	kind := block.MaterialKind
	if block.Kind == aiassistantport.ContentImage {
		kind = "image"
	} else if block.Kind == aiassistantport.ContentMiniProgram {
		kind = "miniprogram"
	} else if block.Kind == aiassistantport.ContentLink {
		kind = "group_invite"
	} else if block.Kind == aiassistantport.ContentAttachment {
		kind = "attachment"
	}
	snapshot, err := a.capturer.CaptureGroupOpsMaterialSources(ctx, mediaport.GroupOpsMaterialPlan{References: []mediaport.GroupOpsMaterialReference{{Kind: kind, ID: block.MaterialID}}})
	if err != nil || len(snapshot.References) != 1 {
		return aiassistantport.ContentBlock{}, errors.New("material unavailable")
	}
	block.MaterialKind = kind
	block.MaterialDigest = effectport.Digest(snapshot.References[0].SourceDigest)
	return block, nil
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
	Attachment(context.Context, int64) (map[string]any, []byte, error)
}
type aiPrivatePayloadReader struct {
	content   aiassistantport.OutboundPayloadReader
	images    aiImageReader
	materials aiMaterialDetails
	uow       platformport.UnitOfWork
	capturer  mediaport.GroupOpsMaterialSourceCapturer
}

// frozenAutomationContent is the versioned object stored by Outbound for an
// accepted automatic message. It holds only fixed text and Media source
// digests/local references; it never stores binary content or Provider IDs.
type frozenAutomationContent struct {
	SchemaVersion int                              `json:"schema_version"`
	ContentText   string                           `json:"content_text,omitempty"`
	Sources       []frozenAutomationMaterialSource `json:"sources,omitempty"`
}

// frozenAutomationMaterialSource excludes Provider-shaped metadata and binary
// content. The captured digest proves the complete Media-owned source when it
// is re-read immediately before the one Provider request.
type frozenAutomationMaterialSource struct {
	Kind         string `json:"kind"`
	ID           int64  `json:"id"`
	SourceDigest string `json:"source_digest"`
}

type automationOutboundContentFreezer struct {
	capturer mediaport.GroupOpsMaterialSourceCapturer
}

func (a automationOutboundContentFreezer) FreezeOutboundContent(ctx context.Context, content automationport.OutboundPublishedContent) (json.RawMessage, [32]byte, error) {
	if a.capturer == nil || content.AgentID < 1 || content.PublishedVersion < 1 || len(content.Content.DynamicMiniprogramCard) != 0 {
		return nil, [32]byte{}, errors.New("automation content freezer unavailable")
	}
	references := make([]mediaport.GroupOpsMaterialReference, 0, len(content.Content.ImageLibraryIDs)+len(content.Content.MiniprogramLibraryIDs)+len(content.Content.AttachmentLibraryIDs)+len(content.Content.GroupInviteLibraryIDs))
	for _, id := range content.Content.ImageLibraryIDs {
		references = append(references, mediaport.GroupOpsMaterialReference{Kind: "image", ID: id})
	}
	for _, id := range content.Content.MiniprogramLibraryIDs {
		references = append(references, mediaport.GroupOpsMaterialReference{Kind: "miniprogram", ID: id})
	}
	for _, id := range content.Content.AttachmentLibraryIDs {
		references = append(references, mediaport.GroupOpsMaterialReference{Kind: "attachment", ID: id})
	}
	for _, id := range content.Content.GroupInviteLibraryIDs {
		references = append(references, mediaport.GroupOpsMaterialReference{Kind: "group_invite", ID: id})
	}
	snapshot := frozenAutomationContent{SchemaVersion: 1, ContentText: strings.TrimSpace(content.Content.ContentText)}
	if len(references) > 0 {
		var err error
		captured, err := a.capturer.CaptureGroupOpsMaterialSources(ctx, mediaport.GroupOpsMaterialPlan{References: references})
		if err != nil || mediaport.ValidateGroupOpsMaterialSourceSnapshot(captured) != nil {
			return nil, [32]byte{}, errors.New("automation content material unavailable")
		}
		snapshot.Sources = make([]frozenAutomationMaterialSource, len(captured.References))
		for index, source := range captured.References {
			snapshot.Sources[index] = frozenAutomationMaterialSource{Kind: source.Reference.Kind, ID: source.Reference.ID, SourceDigest: source.SourceDigest}
		}
	}
	if snapshot.ContentText == "" && len(snapshot.Sources) == 0 {
		return nil, [32]byte{}, errors.New("automation content is empty")
	}
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return nil, [32]byte{}, err
	}
	return raw, sha256.Sum256(raw), nil
}

type automationFrozenPayloadReader struct{ preparer aiPrivatePayloadReader }

func (a automationFrozenPayloadReader) LoadFrozenAutomationMessagePayload(ctx context.Context, raw json.RawMessage, digest [32]byte) (outbound.PrivateMessagePayload, error) {
	var snapshot frozenAutomationContent
	if len(raw) == 0 || json.Unmarshal(raw, &snapshot) != nil || snapshot.SchemaVersion != 1 || sha256.Sum256(mustMarshalFrozenAutomationContent(snapshot)) != digest {
		return outbound.PrivateMessagePayload{}, errors.New("automation content snapshot is invalid")
	}
	references := make([]mediaport.GroupOpsMaterialReference, len(snapshot.Sources))
	for index, source := range snapshot.Sources {
		if !effectport.ValidDigest(effectport.Digest(source.SourceDigest)) {
			return outbound.PrivateMessagePayload{}, errors.New("automation content source digest is invalid")
		}
		references[index] = mediaport.GroupOpsMaterialReference{Kind: source.Kind, ID: source.ID}
	}
	if len(references) > 0 && mediaport.ValidateGroupOpsMaterialPlan(mediaport.GroupOpsMaterialPlan{References: references}) != nil {
		return outbound.PrivateMessagePayload{}, errors.New("automation content sources are invalid")
	}
	blocks := make([]aiassistantport.ContentBlock, 0, 1+len(snapshot.Sources))
	if text := strings.TrimSpace(snapshot.ContentText); text != "" {
		blocks = append(blocks, aiassistantport.ContentBlock{Kind: aiassistantport.ContentText, Text: text})
	}
	for _, source := range snapshot.Sources {
		block := aiassistantport.ContentBlock{MaterialKind: source.Kind, MaterialID: source.ID, MaterialDigest: effectport.Digest(source.SourceDigest)}
		switch source.Kind {
		case "image":
			block.Kind = aiassistantport.ContentImage
		case "miniprogram":
			block.Kind = aiassistantport.ContentMiniProgram
		case "attachment":
			block.Kind = aiassistantport.ContentAttachment
		case "group_invite":
			block.Kind = aiassistantport.ContentLink
		default:
			return outbound.PrivateMessagePayload{}, errors.New("automation content source kind is invalid")
		}
		blocks = append(blocks, block)
	}
	return a.preparer.prepareBlocks(ctx, blocks)
}

func mustMarshalFrozenAutomationContent(value frozenAutomationContent) []byte {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	return raw
}

func (a aiPrivatePayloadReader) verifyFrozenMaterial(ctx context.Context, block aiassistantport.ContentBlock) error {
	if block.Kind == aiassistantport.ContentText || a.uow == nil || a.capturer == nil || !effectport.ValidDigest(block.MaterialDigest) {
		if block.Kind == aiassistantport.ContentText {
			return nil
		}
		return errors.New("frozen material verification unavailable")
	}
	kind := block.MaterialKind
	if block.Kind == aiassistantport.ContentImage {
		kind = "image"
	}
	if block.Kind == aiassistantport.ContentMiniProgram {
		kind = "miniprogram"
	}
	if block.Kind == aiassistantport.ContentLink {
		kind = "group_invite"
	}
	if block.Kind == aiassistantport.ContentAttachment {
		kind = "attachment"
	}
	return a.uow.Within(ctx, func(tx context.Context) error {
		snapshot, err := a.capturer.CaptureGroupOpsMaterialSources(tx, mediaport.GroupOpsMaterialPlan{References: []mediaport.GroupOpsMaterialReference{{Kind: kind, ID: block.MaterialID}}})
		if err != nil || len(snapshot.References) != 1 || effectport.Digest(snapshot.References[0].SourceDigest) != block.MaterialDigest {
			return errors.New("frozen material drift")
		}
		return nil
	})
}

func (a aiPrivatePayloadReader) LoadPrivateMessagePayload(ctx context.Context, reference string, digest effectport.Digest) (outbound.PrivateMessagePayload, error) {
	content, err := a.content.LoadOutboundContent(ctx, reference, digest)
	if err != nil {
		return outbound.PrivateMessagePayload{}, err
	}
	return a.prepareBlocks(ctx, content.Blocks)
}

func (a aiPrivatePayloadReader) prepareBlocks(ctx context.Context, blocks []aiassistantport.ContentBlock) (outbound.PrivateMessagePayload, error) {
	result := outbound.PrivateMessagePayload{}
	for _, block := range blocks {
		if err := a.verifyFrozenMaterial(ctx, block); err != nil {
			return outbound.PrivateMessagePayload{}, err
		}
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
			item, bytes, e := a.materials.Attachment(ctx, block.MaterialID)
			if e != nil || text(item["mime_type"]) != "application/pdf" || len(bytes) == 0 {
				return outbound.PrivateMessagePayload{}, errors.New("PDF attachment unavailable")
			}
			result.Attachments = append(result.Attachments, outbound.PrivateMessageAttachment{Kind: "file", Content: bytes, FileName: text(item["file_name"]), MediaType: text(item["mime_type"])})
		default:
			return outbound.PrivateMessagePayload{}, errors.New("unsupported content kind")
		}
	}
	result.Text = strings.TrimSpace(result.Text)
	return result, nil
}
func text(value any) string { out, _ := value.(string); return out }
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
