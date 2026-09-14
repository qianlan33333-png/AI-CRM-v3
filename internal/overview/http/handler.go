// Package http exposes the admin-only operating overview. It performs Access
// authentication and role gating before any domain aggregate is called.
package http

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	overviewapp "github.com/qianlan33333-png/AI-CRM-v3/internal/overview/app"
)

const overviewPath = "/api/admin/overview"

type RequestSecurity interface {
	Authenticate(context.Context, *http.Request) (accessdomain.Principal, error)
}

type Reader interface {
	Read(context.Context, overviewapp.Query) (overviewapp.Response, error)
}

type Config struct {
	Reader   Reader
	Security RequestSecurity
	Now      func() time.Time
}

type Handler struct {
	reader   Reader
	security RequestSecurity
	now      func() time.Time
}

func NewHandler(config Config) (*Handler, error) {
	if config.Reader == nil || config.Security == nil {
		return nil, errors.New("overview HTTP dependencies are required")
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	return &Handler{reader: config.Reader, security: config.Security, now: config.Now}, nil
}

func (handler *Handler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if handler == nil || handler.reader == nil || handler.security == nil || handler.now == nil {
		writeError(writer, http.StatusServiceUnavailable, "unavailable")
		return
	}
	if request.URL.Path != overviewPath {
		writeError(writer, http.StatusNotFound, "not_found")
		return
	}
	if request.Method != http.MethodGet {
		writer.Header().Set("Allow", http.MethodGet)
		writeError(writer, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	if _, ok := handler.authorize(writer, request); !ok {
		return
	}
	query, ok := parseQuery(request, handler.now())
	if !ok {
		writeError(writer, http.StatusBadRequest, "invalid_request")
		return
	}
	response, err := handler.reader.Read(request.Context(), query)
	if err != nil {
		writeError(writer, http.StatusServiceUnavailable, "unavailable")
		return
	}
	writeJSON(writer, http.StatusOK, response)
}

func (handler *Handler) authorize(writer http.ResponseWriter, request *http.Request) (accessdomain.Principal, bool) {
	principal, err := handler.security.Authenticate(request.Context(), request)
	if err != nil {
		writeError(writer, http.StatusUnauthorized, "unauthorized")
		return accessdomain.Principal{}, false
	}
	// Access's current browser Principal contains an employee role but no
	// per-record data scope. These roles are therefore the existing global
	// admin-read scope used by Distribution's admin read model; no overview
	// handler may manufacture a narrower or broader predicate itself.
	if !canReadAdmin(principal) {
		writeError(writer, http.StatusForbidden, "permission_denied")
		return accessdomain.Principal{}, false
	}
	return principal, true
}

func canReadAdmin(principal accessdomain.Principal) bool {
	if principal.InternalID < 1 || (principal.Kind != accessdomain.KindAdmin && principal.Kind != accessdomain.KindStaff) {
		return false
	}
	for _, role := range principal.Roles {
		if role == accessdomain.RoleViewer || role == accessdomain.RoleAdmin || role == accessdomain.RoleSuperAdmin {
			return true
		}
	}
	return false
}

func parseQuery(request *http.Request, now time.Time) (overviewapp.Query, bool) {
	values := request.URL.Query()
	for key, entries := range values {
		if (key != "period" && key != "from" && key != "to") || len(entries) != 1 {
			return overviewapp.Query{}, false
		}
	}
	period := values.Get("period")
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil || now.IsZero() {
		return overviewapp.Query{}, false
	}
	local := now.In(location)
	startOfToday := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, location)
	start, end := time.Time{}, time.Time{}
	switch period {
	case "today":
		if values.Get("from") != "" || values.Get("to") != "" {
			return overviewapp.Query{}, false
		}
		start, end = startOfToday, startOfToday.AddDate(0, 0, 1)
	case "7d":
		if values.Get("from") != "" || values.Get("to") != "" {
			return overviewapp.Query{}, false
		}
		start, end = startOfToday.AddDate(0, 0, -6), startOfToday.AddDate(0, 0, 1)
	case "30d":
		if values.Get("from") != "" || values.Get("to") != "" {
			return overviewapp.Query{}, false
		}
		start, end = startOfToday.AddDate(0, 0, -29), startOfToday.AddDate(0, 0, 1)
	case "custom":
		from, fromOK := parseDate(values.Get("from"), location)
		to, toOK := parseDate(values.Get("to"), location)
		if !fromOK || !toOK || to.Before(from) {
			return overviewapp.Query{}, false
		}
		start, end = from, to.AddDate(0, 0, 1)
	default:
		return overviewapp.Query{}, false
	}
	return overviewapp.Query{Range: overviewapp.Range{Period: period, Timezone: "Asia/Shanghai", Start: start.UTC(), End: end.UTC()}}, true
}

func parseDate(value string, location *time.Location) (time.Time, bool) {
	if len(value) != len("2006-01-02") || strings.TrimSpace(value) != value {
		return time.Time{}, false
	}
	parsed, err := time.ParseInLocation("2006-01-02", value, location)
	return parsed, err == nil
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}

func writeError(writer http.ResponseWriter, status int, code string) {
	writeJSON(writer, status, map[string]string{"error": code})
}
