package main

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	groupopsport "github.com/qianlan33333-png/AI-CRM-v3/internal/groupops/port"
)

// audienceOperationMemberPicker exposes the Access-owned eligible staff
// directory to the audience sender allowlist. It is deliberately read-only:
// selecting a member only returns the verified WeCom userid; the Segment
// command remains the owner of the persisted sender set.
type audienceOperationMemberPicker struct {
	directory interface {
		ListEligibleStaff(context.Context) ([]groupopsport.OperationMember, error)
	}
	security interface {
		Authenticate(context.Context, *http.Request) (accessdomain.Principal, error)
	}
}

func (picker audienceOperationMemberPicker) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	if picker.directory == nil || picker.security == nil || r.URL.Query().Get("scope") != "audience_senders" {
		writeAudienceOperationMemberError(w, http.StatusNotFound, "not_found")
		return
	}
	principal, err := picker.security.Authenticate(r.Context(), r)
	if err != nil || principal.InternalID < 1 || (principal.Kind != accessdomain.KindAdmin && principal.Kind != accessdomain.KindStaff) {
		writeAudienceOperationMemberError(w, http.StatusUnauthorized, "authentication_required")
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		w.Header().Set("Allow", "GET, POST")
		writeAudienceOperationMemberError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	pageSize := 100
	if raw := r.URL.Query().Get("page_size"); raw != "" {
		pageSize, err = strconv.Atoi(raw)
		if err != nil || pageSize < 1 || pageSize > 100 {
			writeAudienceOperationMemberError(w, http.StatusBadRequest, "invalid_page")
			return
		}
	}
	query := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	items, err := picker.directory.ListEligibleStaff(r.Context())
	if err != nil {
		writeAudienceOperationMemberError(w, http.StatusServiceUnavailable, "staff_directory_unavailable")
		return
	}
	filtered := make([]audienceOperationMember, 0, len(items))
	for _, item := range items {
		if item.StaffID < 1 || strings.TrimSpace(item.SenderUserID) == "" || strings.TrimSpace(item.DisplayName) == "" {
			continue
		}
		if query != "" && !strings.Contains(strings.ToLower(item.SenderUserID), query) && !strings.Contains(strings.ToLower(item.DisplayName), query) {
			continue
		}
		filtered = append(filtered, audienceOperationMember{StaffID: item.StaffID, UserID: item.SenderUserID, DisplayName: item.DisplayName, Active: true})
	}
	sort.SliceStable(filtered, func(i, j int) bool {
		if filtered[i].DisplayName == filtered[j].DisplayName {
			return filtered[i].StaffID < filtered[j].StaffID
		}
		return filtered[i].DisplayName < filtered[j].DisplayName
	})
	if len(filtered) > pageSize {
		filtered = filtered[:pageSize]
	}
	writeAudienceOperationMemberJSON(w, http.StatusOK, map[string]any{"scope": "audience_senders", "page_size": pageSize, "items": filtered})
}

type audienceOperationMember struct {
	StaffID     int64  `json:"staff_id"`
	UserID      string `json:"user_id"`
	DisplayName string `json:"display_name"`
	Active      bool   `json:"active"`
}

func writeAudienceOperationMemberError(w http.ResponseWriter, status int, code string) {
	writeAudienceOperationMemberJSON(w, status, map[string]any{"ok": false, "error": code})
}

func writeAudienceOperationMemberJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
