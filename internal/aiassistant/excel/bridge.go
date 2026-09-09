package excel

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	access "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	accessport "github.com/qianlan33333-png/AI-CRM-v3/internal/access/port"
	app "github.com/qianlan33333-png/AI-CRM-v3/internal/aiassistant/app"
	ai "github.com/qianlan33333-png/AI-CRM-v3/internal/aiassistant/port"
	effect "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	outbound "github.com/qianlan33333-png/AI-CRM-v3/internal/outbound/port"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type Security interface {
	Authenticate(context.Context, *http.Request) (access.Principal, error)
	AuthorizeCSRF(context.Context, *http.Request) (access.Principal, error)
}
type Repository interface {
	ExcelPlans(context.Context) ([]ai.PlanID, error)
	ExcelApproval(context.Context, ai.PlanID) (string, time.Time, error)
	RecordExcelDelivery(context.Context, ai.Recipient, outbound.PrivateMessageDelivery) error
}
type Bridge struct {
	Client     *Client
	App        *app.Service
	Repo       Repository
	Receipts   outbound.PrivateMessageDeliveryStore
	Provider   outbound.PrivateMessageDeliveryReader
	Security   Security
	Authorizer accessport.AIAssistantAuthorizer
	Scope      string
}
type row struct {
	ID      ai.RecipientID    `json:"id"`
	UnionID string            `json:"unionid"`
	Sender  string            `json:"sender_userid"`
	Text    string            `json:"text"`
	Path    string            `json:"path"`
	State   string            `json:"state"`
	Reason  string            `json:"reason"`
	SentAt  *time.Time        `json:"sent_at"`
	Version int64             `json:"version"`
	Content []ai.ContentBlock `json:"content"`
	Review  string            `json:"review_state"`
}

func fmtInt(n int64) string { return strconv.FormatInt(n, 10) }
func (b *Bridge) Rows(ctx context.Context, id ai.PlanID, poll bool) ([]row, error) {
	result := []row{}
	cursor := ""
	for {
		page, err := b.App.ListRecipients(ctx, ai.RecipientPageQuery{PlanID: id, Cursor: cursor, Limit: 50})
		if err != nil {
			return nil, err
		}
		for _, recipient := range page.Items {
			if recipient.DeferredTarget == nil {
				return nil, app.ErrInvalid
			}
			r, content, err := b.App.GetRecipient(ctx, id, recipient.ID)
			if err != nil {
				return nil, err
			}
			item := row{ID: r.ID, UnionID: r.DeferredTarget.UnionID, Sender: r.DeferredTarget.SenderUserID, State: string(r.ExecutionState), Version: r.Version, Content: content.Blocks, Review: string(r.ReviewState)}
			for _, block := range content.Blocks {
				if block.Kind == ai.ContentText {
					item.Text = block.Text
				}
				if block.ExcelCard != nil {
					item.Path = block.ExcelCard.Path
				}
			}
			ref := fmt.Sprintf("aiassistant:%d:%d:%d", id, r.ID, content.ID)
			receipt, found, readErr := b.Receipts.PrivateMessageReceipt(ctx, ref)
			if readErr != nil {
				return nil, readErr
			}
			if poll && found && receipt.MessageID != "" && (r.ExecutionState == ai.ExecutionProviderAccepted || r.ExecutionState == ai.ExecutionOutcomeUnknown) && (receipt.Status == nil || *receipt.Status == 0) {
				updated, err := b.pollReceipt(ctx, receipt)
				if err == nil && updated.Status != nil {
					if err = b.Receipts.SavePrivateMessageDelivery(ctx, ref, updated); err == nil {
						receipt = updated
					}
				} // read failures leave prior truth intact
			}
			if found {
				item.Reason = receipt.Reason
				if receipt.Status != nil {
					if *receipt.Status == 1 && receipt.SentAt != nil {
						item.State = "delivery_proven"
						item.SentAt = receipt.SentAt
					} else if *receipt.Status > 1 {
						item.State = "final_failed"
						item.Reason = fmt.Sprintf("wecom_status_%d", *receipt.Status)
					}
					if poll && item.State != string(r.ExecutionState) && item.State != "provider_accepted" {
						if err = b.Repo.RecordExcelDelivery(ctx, r, receipt); err != nil {
							return nil, err
						}
					}
				}
			}
			result = append(result, item)
		}
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	return result, nil
}
func (b *Bridge) pollReceipt(ctx context.Context, receipt outbound.PrivateMessageDelivery) (outbound.PrivateMessageDelivery, error) {
	cursor := ""
	seen := map[string]bool{}
	var match *outbound.PrivateMessageDelivery
	for {
		if seen[cursor] {
			return receipt, errors.New("receipt cursor repeated")
		}
		seen[cursor] = true
		page, err := b.Provider.GetPrivateMessageSendResult(ctx, receipt.MessageID, receipt.SenderUserID, cursor)
		if err != nil {
			return receipt, err
		}
		for _, item := range page.Items {
			if item.ExternalUserID == receipt.ExternalUserID && item.SenderUserID == receipt.SenderUserID && item.MessageID == receipt.MessageID {
				if match != nil {
					return receipt, errors.New("ambiguous receipt")
				}
				copy := item
				match = &copy
			}
		}
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	if match == nil {
		return receipt, errors.New("receipt not observed")
	}
	return *match, nil
}
func (b *Bridge) PrepareSnapshot(ctx context.Context, id ai.PlanID, version int64) (string, error) {
	if b.Client == nil {
		return "", ErrUnavailable
	}
	rows, err := b.Rows(ctx, id, false)
	if err != nil {
		return "", err
	}
	var random [16]byte
	if _, err = rand.Read(random[:]); err != nil {
		return "", err
	}
	key := snapshotKey(id, version) + ":" + hex.EncodeToString(random[:])
	targets := make([]map[string]any, 0, len(rows))
	for _, r := range rows {
		targets = append(targets, map[string]any{"id": r.ID, "unionid": r.UnionID})
	}
	err = b.Client.JSON(ctx, "/snapshots", map[string]any{"snapshot_key": key, "rows": targets}, nil)
	return key, err
}
func (b *Bridge) Refresh(ctx context.Context) error {
	if b.Client == nil {
		return nil
	}
	ids, err := b.Repo.ExcelPlans(ctx)
	if err != nil {
		return err
	}
	var first error
	for _, id := range ids {
		key, _, err := b.Repo.ExcelApproval(ctx, id)
		if err != nil {
			if first == nil {
				first = err
			}
			continue
		}
		rows, err := b.Rows(ctx, id, true)
		if err == nil {
			for i := range rows {
				rows[i].Content = nil
			}
			err = b.Client.JSON(ctx, "/observations", map[string]any{"plan_id": id, "snapshot_key": key, "rows": rows}, nil)
		}
		if err != nil && first == nil {
			first = err
		}
	}
	return first
}
func (b *Bridge) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	action := accessport.AIAssistantRead
	if r.Method != "GET" {
		action = accessport.AIAssistantReview
	}
	if strings.HasSuffix(r.URL.Path, "/approve") {
		action = accessport.AIAssistantApprove
	}
	var actor access.Principal
	var err error
	if r.Method == "GET" {
		actor, err = b.Security.Authenticate(r.Context(), r)
	} else {
		actor, err = b.Security.AuthorizeCSRF(r.Context(), r)
	}
	if err != nil || b.Authorizer.AuthorizeAIAssistant(r.Context(), actor, action) != nil {
		respond(w, 403, map[string]any{"error": "permission_denied"})
		return
	}
	if b.Client == nil && (r.Method != "GET" || strings.HasSuffix(r.URL.Path, "/report") || strings.Contains(r.URL.Path, "/covers/")) {
		respond(w, 503, map[string]any{"error": "component_disabled"})
		return
	}
	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/admin/operation-batches"), "/"), "/")
	var output any
	switch {
	case r.Method == "POST" && len(parts) == 1 && parts[0] == "imports":
		raw, e := io.ReadAll(http.MaxBytesReader(w, r.Body, 8<<20))
		if e != nil {
			err = app.ErrInvalid
			break
		}
		key := r.Header.Get("Idempotency-Key")
		path := "/imports"
		if r.URL.Query().Get("new") == "1" {
			path += "?new=1"
		}
		var prepared struct {
			BatchKey   string        `json:"batch_key"`
			FileDigest effect.Digest `json:"file_digest"`
			CreatedAt  time.Time     `json:"created_at"`
			Rows       []struct {
				UnionID string       `json:"unionid"`
				Text    string       `json:"text"`
				Sender  string       `json:"sender_userid"`
				Card    ai.ExcelCard `json:"card"`
			} `json:"rows"`
		}
		err = b.Client.Call(r.Context(), "POST", path, key, raw, &prepared)
		if err != nil {
			break
		}
		command := ai.CreatePlanCommand{Actor: ai.Actor{Kind: ai.ActorAdmin, ID: actor.InternalID}, IdempotencyKey: "excel-" + prepared.BatchKey, Name: "Excel 群发批次 " + prepared.CreatedAt.In(time.FixedZone("CST", 8*3600)).Format("2006-01-02 15:04"), SourceKind: "excel_batch", SourceDigest: prepared.FileDigest, OccurredAt: prepared.CreatedAt}
		for _, v := range prepared.Rows {
			card := v.Card
			command.Recipients = append(command.Recipients, ai.RecipientCandidate{DeferredTarget: &ai.DeferredTarget{UnionID: v.UnionID, Scope: b.Scope, SenderUserID: v.Sender}, Content: []ai.ContentBlock{{Kind: ai.ContentText, Text: v.Text}, {Kind: ai.ContentMiniProgram, ExcelCard: &card}}})
		}
		var created ai.CreatePlanResult
		created, err = b.App.CreateExcelPlan(r.Context(), prepared.BatchKey, command)
		if err == nil {
			_ = b.Client.JSON(r.Context(), "/link", map[string]any{"batch_key": prepared.BatchKey, "plan_id": created.Plan.ID}, nil)
			output = map[string]any{"plan": created.Plan, "replayed": created.Replayed}
		}
	case r.Method == "GET" && len(parts) == 1 && parts[0] == "":
		var ids []ai.PlanID
		ids, err = b.Repo.ExcelPlans(r.Context())
		plans := []ai.Plan{}
		for i := len(ids) - 1; i >= 0 && err == nil; i-- {
			var p ai.Plan
			p, err = b.App.GetPlan(r.Context(), ids[i])
			plans = append(plans, p)
		}
		output = map[string]any{"items": plans}
	case r.Method == "GET" && len(parts) == 2 && parts[0] == "covers":
		var raw []byte
		err = b.Client.Call(r.Context(), "GET", "/covers/"+parts[1], "", nil, &raw)
		if err == nil {
			w.Header().Set("Content-Type", http.DetectContentType(raw))
			w.Header().Set("Cache-Control", "private, max-age=3600")
			w.Write(raw)
			return
		}
	default:
		n, e := strconv.ParseInt(parts[0], 10, 64)
		if e != nil || n < 1 {
			err = app.ErrInvalid
			break
		}
		id := ai.PlanID(n)
		var plan ai.Plan
		plan, err = b.App.GetPlan(r.Context(), id)
		if err != nil {
			break
		}
		if plan.SourceKind != "excel_batch" {
			err = app.ErrInvalid
			break
		}
		switch {
		case r.Method == "GET" && len(parts) == 1:
			var rows []row
			rows, err = b.Rows(r.Context(), id, false)
			output = map[string]any{"plan": plan, "rows": rows}
		case r.Method == "GET" && len(parts) == 2 && parts[1] == "report":
			var report json.RawMessage
			err = b.Client.Call(r.Context(), "GET", "/reports/"+fmtInt(n), "", nil, &report)
			output = report
		case r.Method == "POST" && len(parts) == 2 && parts[1] == "cover":
			version, e := strconv.ParseInt(r.URL.Query().Get("expected_version"), 10, 64)
			if e != nil || version < 1 {
				err = app.ErrInvalid
				break
			}
			raw, e := io.ReadAll(http.MaxBytesReader(w, r.Body, 2<<20))
			if e != nil {
				err = app.ErrInvalid
				break
			}
			imageInfo, _, imageErr := image.DecodeConfig(bytes.NewReader(raw))
			if imageErr != nil || imageInfo.Width < 1 || imageInfo.Height < 1 || int64(imageInfo.Width)*int64(imageInfo.Height) > 40000000 {
				err = &InputError{Message: "请上传有效的 PNG 或 JPEG 封面（不超过 2 MB）"}
				break
			}
			var saved struct {
				Digest effect.Digest `json:"cover_digest"`
			}
			err = b.Client.Call(r.Context(), "POST", "/covers", "", raw, &saved)
			if err != nil {
				break
			}
			plan, err = b.App.ApplyExcelCover(r.Context(), ai.Actor{Kind: ai.ActorAdmin, ID: actor.InternalID}, id, version, r.Header.Get("Idempotency-Key"), saved.Digest)
			output = map[string]any{"plan": plan, "cover_digest": saved.Digest}
		case r.Method == "POST" && len(parts) == 2 && parts[1] == "approve":
			var input struct {
				Version int64 `json:"expected_version"`
			}
			err = json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&input)
			if err != nil {
				break
			}
			if plan.State != "pending_review" && plan.State != "partially_approved" {
				output = map[string]any{"plan": plan}
				break
			}
			var preview ai.ApprovalPreview
			who := ai.Actor{Kind: ai.ActorAdmin, ID: actor.InternalID}
			preview, err = b.App.PreviewApproval(r.Context(), ai.PreviewApprovalCommand{Actor: who, PlanID: id, ExpectedVersion: input.Version})
			if err != nil {
				break
			}
			plan, err = b.App.ApprovePlan(r.Context(), ai.ApprovePlanCommand{Actor: who, PlanID: id, ExpectedVersion: input.Version, PreviewDigest: preview.PreviewDigest, IdempotencyKey: r.Header.Get("Idempotency-Key")})
			output = map[string]any{"plan": plan}
		default:
			err = app.ErrNotFound
		}
	}
	if err != nil {
		var inputError *InputError
		if errors.As(err, &inputError) {
			respond(w, 400, map[string]any{"error": "invalid_input", "message": inputError.Message})
			return
		}
		status := 503
		if errors.Is(err, app.ErrInvalid) {
			status = 400
		}
		if errors.Is(err, app.ErrConflict) {
			status = 409
		}
		if errors.Is(err, app.ErrNotFound) {
			status = 404
		}
		respond(w, status, map[string]any{"error": "batch_request_failed"})
		return
	}
	respond(w, 200, output)
}
func respond(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
