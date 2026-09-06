package http

import (
	"context"
	"encoding/json"
	"io"
	nethttp "net/http"
	"strings"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	customerapp "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/app"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
)

type ownerHandoffPreviewBody struct {
	Mode               string  `json:"mode"`
	SourceStaffID      int64   `json:"source_staff_id"`
	TargetStaffID      int64   `json:"target_staff_id"`
	CorpScope          string  `json:"corp_scope"`
	CustomerIDs        []int64 `json:"customer_ids"`
	WelcomeMessage     string  `json:"welcome_message"`
	ConfirmationPhrase string  `json:"confirmation_phrase"`
	IdempotencyKey     string  `json:"idempotency_key"`
}
type ownerHandoffConfirmBody struct {
	PreviewID          string `json:"preview_id"`
	PreviewHash        string `json:"preview_hash"`
	ConfirmationPhrase string `json:"confirmation_phrase"`
	IdempotencyKey     string `json:"idempotency_key"`
}
type ownerHandoffTransferResultBody struct {
	IdempotencyKey string `json:"idempotency_key"`
}

func (handler *Handler) ownerHandoffPrincipal(response nethttp.ResponseWriter, request *nethttp.Request, csrf bool) (accessdomain.Principal, bool) {
	var principal accessdomain.Principal
	var err error
	if csrf {
		principal, err = handler.csrf.AuthorizeCSRF(request.Context(), request)
	} else {
		principal, err = handler.auth.Authenticate(request.Context(), request)
	}
	if err != nil {
		handler.writeError(response, err)
		return accessdomain.Principal{}, false
	}
	if !principal.IsSuperAdmin() {
		handler.writeError(response, accessdomain.ErrPermissionDenied)
		return accessdomain.Principal{}, false
	}
	return principal, true
}
func decodeOwnerHandoff(request *nethttp.Request, out any) error {
	decoder := json.NewDecoder(io.LimitReader(request.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		return customerapp.ErrInvalidQuery
	}
	if decoder.More() {
		return customerapp.ErrInvalidQuery
	}
	return nil
}
func (handler *Handler) ownerHandoffPreview(response nethttp.ResponseWriter, request *nethttp.Request) {
	principal, ok := handler.ownerHandoffPrincipal(response, request, true)
	if !ok {
		return
	}
	var body ownerHandoffPreviewBody
	if err := decodeOwnerHandoff(request, &body); err != nil {
		handler.writeError(response, err)
		return
	}
	ids := make([]customerdomain.CustomerID, len(body.CustomerIDs))
	for i, id := range body.CustomerIDs {
		ids[i] = customerdomain.CustomerID(id)
	}
	preview, err := handler.ownerHandoff.PreviewOwnerHandoff(request.Context(), customerport.OwnerHandoffPreviewCommand{ActorAdminUserID: principal.InternalID, Mode: customerport.OwnerHandoffMode(body.Mode), SourceStaffID: body.SourceStaffID, TargetStaffID: body.TargetStaffID, CorpScope: body.CorpScope, CustomerIDs: ids, WelcomeMessage: body.WelcomeMessage, ConfirmationPhrase: body.ConfirmationPhrase, IdempotencyKey: body.IdempotencyKey})
	if err != nil {
		handler.writeError(response, err)
		return
	}
	writePrivateJSON(response, nethttp.StatusOK, preview)
}
func (handler *Handler) ownerHandoffConfirm(response nethttp.ResponseWriter, request *nethttp.Request) {
	principal, ok := handler.ownerHandoffPrincipal(response, request, true)
	if !ok {
		return
	}
	var body ownerHandoffConfirmBody
	if err := decodeOwnerHandoff(request, &body); err != nil {
		handler.writeError(response, err)
		return
	}
	batch, err := handler.ownerHandoff.ConfirmOwnerHandoff(request.Context(), customerport.OwnerHandoffConfirmCommand{ActorAdminUserID: principal.InternalID, PreviewID: body.PreviewID, PreviewHash: body.PreviewHash, ConfirmationPhrase: body.ConfirmationPhrase, IdempotencyKey: body.IdempotencyKey})
	if err != nil {
		handler.writeError(response, err)
		return
	}
	writePrivateJSON(response, nethttp.StatusAccepted, batch)
}
func (handler *Handler) ownerHandoffPreviewRead(response nethttp.ResponseWriter, request *nethttp.Request) {
	if _, ok := handler.ownerHandoffPrincipal(response, request, false); !ok {
		return
	}
	id := strings.TrimSpace(request.PathValue("preview_id"))
	var preview customerport.OwnerHandoffPreview
	err := handler.uow.Within(request.Context(), func(tx context.Context) error {
		var e error
		preview, e = handler.ownerHandoffReader.OwnerHandoffPreview(tx, id)
		return e
	})
	if err != nil {
		handler.writeError(response, err)
		return
	}
	writePrivateJSON(response, nethttp.StatusOK, preview)
}
func (handler *Handler) ownerHandoffBatchRead(response nethttp.ResponseWriter, request *nethttp.Request) {
	if _, ok := handler.ownerHandoffPrincipal(response, request, false); !ok {
		return
	}
	id := strings.TrimSpace(request.PathValue("batch_id"))
	var batch customerport.OwnerHandoffBatch
	err := handler.uow.Within(request.Context(), func(tx context.Context) error {
		var e error
		batch, e = handler.ownerHandoffReader.OwnerHandoffBatch(tx, id)
		return e
	})
	if err != nil {
		handler.writeError(response, err)
		return
	}
	writePrivateJSON(response, nethttp.StatusOK, batch)
}

func (handler *Handler) ownerHandoffTransferResult(response nethttp.ResponseWriter, request *nethttp.Request) {
	principal, ok := handler.ownerHandoffPrincipal(response, request, true)
	if !ok {
		return
	}
	var body ownerHandoffTransferResultBody
	if err := decodeOwnerHandoff(request, &body); err != nil {
		handler.writeError(response, err)
		return
	}
	batchID := strings.TrimSpace(request.PathValue("batch_id"))
	batch, err := handler.ownerHandoffTransfers.RefreshOwnerHandoffTransferResult(request.Context(), customerport.OwnerHandoffTransferResultCommand{ActorAdminUserID: principal.InternalID, BatchID: batchID, IdempotencyKey: body.IdempotencyKey})
	if err != nil {
		handler.writeError(response, err)
		return
	}
	writePrivateJSON(response, nethttp.StatusOK, batch)
}
