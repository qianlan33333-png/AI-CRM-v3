package http

import (
	"encoding/json"
	segmentport "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/port"
	"io"
	"net/http"
	"strconv"
	"strings"
)

func (handler *Handler) coreSupervisedPush(w http.ResponseWriter, r *http.Request) {
	principal, ok := handler.machinePrincipal(w, r, "external_integration", "write", "external_write")
	if !ok {
		return
	}
	if handler.coreSupervision == nil {
		writeJSON(w, 503, map[string]string{"error": "core_supervision_unavailable"})
		return
	}
	var input segmentport.CorePush
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	d.DisallowUnknownFields()
	if d.Decode(&input) != nil {
		writeJSON(w, 400, map[string]string{"error": "invalid_request"})
		return
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		writeJSON(w, 400, map[string]string{"error": "invalid_request"})
		return
	}
	if !principal.OwnerScope.Allows(map[string]string{"customer_id": strconv.FormatInt(input.CustomerID, 10), "package_id": strconv.FormatInt(input.PackageID, 10)}) {
		writeJSON(w, 403, map[string]string{"error": "owner_scope_denied"})
		return
	}
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if len(key) < 16 || len(key) > 128 {
		writeJSON(w, 400, map[string]string{"error": "invalid_idempotency_key"})
		return
	}
	result, e := handler.coreSupervision.RecordSupervisedPush(r.Context(), principal.ClientID, key, input)
	if e != nil { // Do not return persistence or provider details to an external node.
		status, code := 503, "core_supervision_unavailable"
		switch e.Error() {
		case "invalid audience configuration request":
			status, code = 400, "invalid_request"
		case "audience configuration conflict":
			status, code = 409, "push_conflict"
		case "audience configuration not found":
			status, code = 404, "not_found"
		}
		writeJSON(w, status, map[string]string{"error": code})
		return
	}
	writeJSON(w, 200, map[string]any{"data": result})
}

func (handler *Handler) coreOperationsRead(w http.ResponseWriter, r *http.Request) {
	principal, ok := handler.machinePrincipal(w, r, "external_integration", "read", "external_read")
	if !ok {
		return
	}
	reader, ok := handler.coreSupervision.(segmentport.CoreOperationsReader)
	if !ok {
		writeJSON(w, 503, map[string]string{"error": "core_operations_unavailable"})
		return
	}
	if r.URL.Path == "/open/v1/audience/core-products" {
		products, e := reader.Products(r.Context())
		if e != nil {
			writeJSON(w, 503, map[string]string{"error": "core_operations_unavailable"})
			return
		}
		visible := []segmentport.CoreProduct{}
		for _, p := range products {
			if principal.OwnerScope.Allows(map[string]string{"package_id": strconv.FormatInt(p.PackageID, 10)}) {
				visible = append(visible, p)
			}
		}
		writeJSON(w, 200, map[string]any{"items": visible})
		return
	}
	packageID, e := strconv.ParseInt(r.PathValue("package_id"), 10, 64)
	if e != nil || packageID < 1 {
		writeJSON(w, 400, map[string]string{"error": "invalid_package"})
		return
	}
	scope := map[string]string{"package_id": strconv.FormatInt(packageID, 10)}
	customerID := int64(0)
	if raw := r.PathValue("customer_id"); raw != "" {
		customerID, e = strconv.ParseInt(raw, 10, 64)
		if e != nil || customerID < 1 {
			writeJSON(w, 400, map[string]string{"error": "invalid_customer"})
			return
		}
		scope["customer_id"] = raw
	}
	if !principal.OwnerScope.Allows(scope) {
		writeJSON(w, 403, map[string]string{"error": "owner_scope_denied"})
		return
	}
	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		limit, e = strconv.Atoi(raw)
	}
	if e != nil || limit < 1 || limit > 100 {
		writeJSON(w, 400, map[string]string{"error": "invalid_limit"})
		return
	}
	var result any
	if customerID > 0 && strings.HasSuffix(r.URL.Path, "/history") {
		result, e = reader.MemberHistory(r.Context(), packageID, customerID, r.URL.Query().Get("cursor"), limit)
	} else if customerID > 0 {
		result, e = reader.MemberDetail(r.Context(), packageID, customerID, r.URL.Query().Get("cursor"), limit)
	} else {
		result, e = reader.CoreMembers(r.Context(), packageID, r.URL.Query().Get("cursor"), limit)
	}
	if e != nil {
		writeJSON(w, 503, map[string]string{"error": "core_operations_unavailable"})
		return
	}
	writeJSON(w, 200, map[string]any{"data": result})
}
