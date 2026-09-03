package http

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	surveyport "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/port"
)

const maxBody = 2 << 20

type RequestSecurity interface {
	Authenticate(context.Context, *http.Request) (accessdomain.Principal, error)
	AuthorizeCSRF(context.Context, *http.Request) (accessdomain.Principal, error)
}
type OAuthApplication interface {
	Enabled() bool
	Start(context.Context, string, string) (string, error)
	Complete(context.Context, string, string) (string, string, error)
	ResolveSession(context.Context, string) (surveyport.SubmissionIdentity, error)
}
type Handler struct {
	definitions surveyport.DefinitionApplication
	submissions interface {
		surveyport.PublicApplication
		surveyport.SubmissionApplication
	}
	security RequestSecurity
	oauth    OAuthApplication
}

func NewHandler(definitions surveyport.DefinitionApplication, submissions interface {
	surveyport.PublicApplication
	surveyport.SubmissionApplication
}, security RequestSecurity, oauth ...OAuthApplication) (*Handler, error) {
	if definitions == nil || submissions == nil || security == nil {
		return nil, errors.New("survey HTTP dependencies are required")
	}
	if len(oauth) > 1 {
		return nil, errors.New("at most one survey OAuth application")
	}
	var oauthApp OAuthApplication
	if len(oauth) == 1 {
		oauthApp = oauth[0]
	}
	return &Handler{definitions: definitions, submissions: submissions, security: security, oauth: oauthApp}, nil
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimSuffix(r.URL.Path, "/")
	switch {
	case path == "/api/admin/questionnaires":
		h.adminRoot(w, r)
	case path == "/api/admin/questionnaires/preflight":
		h.preflight(w, r)
	case strings.HasPrefix(path, "/api/admin/questionnaires/"):
		h.adminTail(w, r, strings.TrimPrefix(path, "/api/admin/questionnaires/"))
	case strings.HasPrefix(path, "/api/public/questionnaires/"):
		h.publicQuestionnaire(w, r, strings.TrimPrefix(path, "/api/public/questionnaires/"))
	case path == "/api/public/survey-submission-results/query":
		h.publicResult(w, r)
	case path == "/api/sidebar/v2/questionnaires":
		h.sidebar(w, r)
	case path == "/api/h5/surveys/oauth/start":
		h.oauthStart(w, r)
	case path == "/api/h5/surveys/oauth/callback":
		h.oauthCallback(w, r)
	case strings.HasPrefix(path, "/api/v1/customers/") && strings.HasSuffix(path, "/survey-answers"):
		h.customerHistory(w, r)
	case path == "/api/admin/survey-history/submissions":
		h.unresolved(w, r)
	case strings.HasPrefix(path, "/api/admin/survey-history/submissions/"):
		h.unresolvedTail(w, r, strings.TrimPrefix(path, "/api/admin/survey-history/submissions/"))
	case strings.HasPrefix(path, "/admin/questionnaires/"):
		h.legacyAdminTail(w, r, strings.TrimPrefix(path, "/admin/questionnaires/"))
	default:
		writeError(w, http.StatusNotFound, "not_found")
	}
}

type definitionRequest struct {
	Name              string                       `json:"name"`
	Title             string                       `json:"title"`
	Description       string                       `json:"description"`
	AnswerDisplayMode surveyport.AnswerDisplayMode `json:"answer_display_mode"`
	AssessmentEnabled bool                         `json:"assessment_enabled"`
	AssessmentConfig  json.RawMessage              `json:"assessment_config"`
	Slug              string                       `json:"slug"`
	IsDisabled        bool                         `json:"is_disabled"`
	Questions         []surveyport.Question        `json:"questions"`
	ScoreRules        []surveyport.ScoreRule       `json:"score_rules"`
}

func (r definitionRequest) questionnaire() surveyport.Questionnaire {
	mode := surveyport.ModeSurvey
	if r.AssessmentEnabled {
		mode = surveyport.ModeAssessment
	}
	status := surveyport.StatusDraft
	if r.IsDisabled {
		status = surveyport.StatusDisabled
	}
	return surveyport.Questionnaire{Name: r.Name, Title: r.Title, Description: r.Description, Mode: mode, AnswerDisplayMode: r.AnswerDisplayMode, AssessmentConfig: r.AssessmentConfig, Slug: r.Slug, Status: status, Questions: r.Questions, ScoreRules: r.ScoreRules}
}

func (h *Handler) adminRoot(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		if !h.read(w, r) {
			return
		}
		limit, offset, ok := pageParams(r, 200)
		if !ok {
			writeError(w, 400, "invalid_request")
			return
		}
		var status surveyport.QuestionnaireStatus
		if raw := r.URL.Query().Get("status"); raw != "" && raw != "all" {
			if raw == "active" {
				status = surveyport.StatusPublished
			} else {
				status = surveyport.QuestionnaireStatus(raw)
			}
		}
		page, err := h.definitions.List(r.Context(), limit, offset, r.URL.Query().Get("q"), status)
		if err != nil {
			resultError(w, err)
			return
		}
		items := make([]any, 0, len(page.Items))
		for _, q := range page.Items {
			items = append(items, definitionResponse(q))
		}
		writeJSON(w, 200, map[string]any{"ok": true, "questionnaires": items, "items": items, "data": map[string]any{"questionnaires": items}, "total": page.Total, "limit": page.Limit, "offset": page.Offset})
	case http.MethodPost:
		principal, ok := h.write(w, r)
		if !ok {
			return
		}
		var body definitionRequest
		if decode(r, &body) != nil {
			writeError(w, 400, "invalid_request")
			return
		}
		q, err := h.definitions.Create(r.Context(), surveyport.CreateCommand{Questionnaire: body.questionnaire(), ActorID: principal.InternalID, IdempotencyKey: idempotency(r)})
		if err != nil {
			resultError(w, err)
			return
		}
		writeJSON(w, 200, definitionEnvelope(q, "created", 0))
	default:
		method(w, "GET, POST")
	}
}

func (h *Handler) adminTail(w http.ResponseWriter, r *http.Request, tail string) {
	parts := strings.Split(tail, "/")
	id, err := parseID(parts[0])
	if err != nil {
		writeError(w, 404, "questionnaire_not_found")
		return
	}
	if len(parts) == 1 {
		switch r.Method {
		case http.MethodGet:
			if !h.read(w, r) {
				return
			}
			q, e := h.definitions.Get(r.Context(), surveyport.ID(id))
			if e != nil {
				resultError(w, e)
				return
			}
			writeJSON(w, 200, definitionEnvelope(q, "", 0))
		case http.MethodPut:
			principal, ok := h.write(w, r)
			if !ok {
				return
			}
			var body definitionRequest
			if decode(r, &body) != nil {
				writeError(w, 400, "invalid_request")
				return
			}
			current, e := h.definitions.Get(r.Context(), surveyport.ID(id))
			if e != nil {
				resultError(w, e)
				return
			}
			value := body.questionnaire()
			value.ID = surveyport.ID(id)
			value.CreatedBy = current.CreatedBy
			value.Version = current.Version
			q, e := h.definitions.Update(r.Context(), surveyport.UpdateCommand{Questionnaire: value, ExpectedVersion: current.Version, ActorID: principal.InternalID, IdempotencyKey: idempotency(r)})
			if e != nil {
				resultError(w, e)
				return
			}
			writeJSON(w, 200, definitionEnvelope(q, "updated", 0))
		case http.MethodDelete:
			principal, ok := h.write(w, r)
			if !ok {
				return
			}
			current, e := h.definitions.Get(r.Context(), surveyport.ID(id))
			if e != nil {
				resultError(w, e)
				return
			}
			e = h.definitions.DeleteDraft(r.Context(), surveyport.ID(id), current.Version, principal.InternalID, idempotency(r))
			if e != nil {
				resultError(w, e)
				return
			}
			writeJSON(w, 200, map[string]any{"ok": true, "questionnaire_id": id, "write_model_status": "deleted", "deleted": true, "delete_mode": "hard_delete"})
		default:
			method(w, "GET, PUT, DELETE")
		}
		return
	}
	if len(parts) >= 4 && parts[1] == "submissions" && parts[3] == "external-push" {
		if _, e := parseID(parts[2]); e != nil {
			writeError(w, 404, "not_found")
			return
		}
		if len(parts) == 4 || len(parts) == 5 && parts[4] == "reconcile" {
			h.operationsDisabled(w, r, id)
			return
		}
	}
	action := strings.Join(parts[1:], "/")
	switch action {
	case "duplicate":
		h.duplicate(w, r, id)
	case "disable":
		h.setStatus(w, r, id, surveyport.StatusDisabled, false)
	case "enable":
		h.setStatus(w, r, id, surveyport.StatusPublished, false)
	case "public-publish":
		h.setStatus(w, r, id, surveyport.StatusPublished, true)
	case "public-disable":
		h.setStatus(w, r, id, surveyport.StatusDisabled, false)
	case "results", "analysis", "public-analytics":
		h.analytics(w, r, id)
	case "submissions":
		h.submissionList(w, r, id, false)
	case "export/preview":
		h.submissionList(w, r, id, false)
	case "export":
		h.export(w, r, id)
	case "operations", "operations/completion", "operations/external-push", "operations/external-push/test":
		h.operationsDisabled(w, r, id)
	default:
		if len(parts) == 3 && parts[1] == "submissions" && parts[2] != "" {
			sid, e := parseID(parts[2])
			if e == nil {
				h.submissionDetail(w, r, id, sid)
				return
			}
		}
		writeError(w, 404, "not_found")
	}
}

func (h *Handler) duplicate(w http.ResponseWriter, r *http.Request, id int64) {
	if r.Method != http.MethodPost {
		method(w, "POST")
		return
	}
	p, ok := h.write(w, r)
	if !ok {
		return
	}
	q, err := h.definitions.Duplicate(r.Context(), surveyport.ID(id), p.InternalID, idempotency(r))
	if err != nil {
		resultError(w, err)
		return
	}
	writeJSON(w, 200, definitionEnvelope(q, "duplicated", id))
}
func (h *Handler) setStatus(w http.ResponseWriter, r *http.Request, id int64, status surveyport.QuestionnaireStatus, publish bool) {
	if r.Method != http.MethodPost {
		method(w, "POST")
		return
	}
	p, ok := h.write(w, r)
	if !ok {
		return
	}
	current, err := h.definitions.Get(r.Context(), surveyport.ID(id))
	if err != nil {
		resultError(w, err)
		return
	}
	var q surveyport.Questionnaire
	if publish {
		q, err = h.definitions.Publish(r.Context(), surveyport.ID(id), current.Version, p.InternalID, idempotency(r))
	} else {
		q, err = h.definitions.SetStatus(r.Context(), surveyport.ID(id), current.Version, status, p.InternalID, idempotency(r))
	}
	if err != nil {
		resultError(w, err)
		return
	}
	state := "enabled"
	if status == surveyport.StatusDisabled {
		state = "disabled"
	}
	writeJSON(w, 200, definitionEnvelope(q, state, 0))
}
func (h *Handler) analytics(w http.ResponseWriter, r *http.Request, id int64) {
	if r.Method != http.MethodGet {
		method(w, "GET")
		return
	}
	if !h.read(w, r) {
		return
	}
	a, err := h.submissions.Analytics(r.Context(), surveyport.ID(id))
	if err != nil {
		resultError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "questionnaire_id": id, "results": map[string]any{"submission_count": a.SubmissionCount, "latest_submitted_at": nil, "average_score": a.AverageScore, "rules": []any{}}, "data": a, "side_effect_executed": false})
}

func (h *Handler) publicQuestionnaire(w http.ResponseWriter, r *http.Request, tail string) {
	parts := strings.Split(tail, "/")
	slug := parts[0]
	if len(parts) == 1 && r.Method == http.MethodGet {
		q, err := h.submissions.ReadPublic(r.Context(), slug)
		if err != nil {
			resultError(w, err)
			return
		}
		writeJSON(w, 200, publicDefinition(q))
		return
	}
	if len(parts) == 2 && parts[1] == "submissions" && r.Method == http.MethodPost {
		var body struct {
			Version       int64                         `json:"version"`
			SubmissionKey string                        `json:"submission_key"`
			Answers       []surveyport.SubmissionAnswer `json:"answers"`
			SourceChannel string                        `json:"source_channel,omitempty"`
			CampaignID    string                        `json:"campaign_id,omitempty"`
			StaffID       string                        `json:"staff_id,omitempty"`
		}
		if decode(r, &body) != nil {
			writeError(w, 400, "invalid_public_input")
			return
		}
		identity := surveyport.SubmissionIdentity{State: surveyport.IdentityAnonymous}
		if h.oauth != nil {
			if cookie, cookieErr := r.Cookie("aicrm_survey_identity"); cookieErr == nil {
				identity, _ = h.oauth.ResolveSession(r.Context(), cookie.Value)
			}
		}
		receipt, err := h.submissions.Submit(r.Context(), surveyport.SubmitCommand{Slug: slug, DefinitionVersion: body.Version, SubmissionKey: body.SubmissionKey, Answers: body.Answers, Identity: identity, SourceChannel: body.SourceChannel, CampaignID: body.CampaignID, StaffID: body.StaffID})
		if err != nil {
			resultError(w, err)
			return
		}
		writeJSON(w, 201, map[string]any{"receipt": receipt, "result_token": receipt.ResultToken})
		return
	}
	method(w, "GET or POST")
}
func (h *Handler) publicResult(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		method(w, "POST")
		return
	}
	var body struct {
		ResultToken string `json:"result_token"`
	}
	if decode(r, &body) != nil {
		writeError(w, 400, "invalid_public_input")
		return
	}
	s, err := h.submissions.QueryResult(r.Context(), body.ResultToken)
	if err != nil {
		resultError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"submission_id": s.ID, "definition_version": s.DefinitionVersion, "submitted_at": s.SubmittedAt, "local_only": true, "external_executed": false, "questionnaire_title": s.QuestionnaireTitle, "mode": s.Mode, "total_score": s.TotalScore, "assessment_result": s.Result})
}

func (h *Handler) oauthStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		method(w, "GET")
		return
	}
	if h.oauth == nil || !h.oauth.Enabled() {
		writeError(w, http.StatusServiceUnavailable, "survey_oauth_disabled")
		return
	}
	location, err := h.oauth.Start(r.Context(), r.URL.Query().Get("slug"), r.URL.Query().Get("display"))
	if err != nil {
		resultError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, location, http.StatusSeeOther)
}

func (h *Handler) oauthCallback(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		method(w, "GET")
		return
	}
	if h.oauth == nil || !h.oauth.Enabled() {
		http.Redirect(w, r, "/h5/error.html?code=survey_oauth_disabled", http.StatusSeeOther)
		return
	}
	session, redirect, err := h.oauth.Complete(r.Context(), r.URL.Query().Get("state"), r.URL.Query().Get("code"))
	if err != nil {
		http.Redirect(w, r, "/h5/error.html?code=survey_oauth_failed", http.StatusSeeOther)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "aicrm_survey_identity", Value: session, Path: "/api/public/questionnaires/", MaxAge: 1800, HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode})
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, redirect, http.StatusSeeOther)
}

func (h *Handler) submissionList(w http.ResponseWriter, r *http.Request, id int64, _ bool) {
	if r.Method != http.MethodGet {
		method(w, "GET")
		return
	}
	if !h.read(w, r) {
		return
	}
	limit, offset, ok := pageParams(r, 100)
	if !ok {
		writeError(w, 400, "invalid_request")
		return
	}
	state := surveyport.IdentityState(r.URL.Query().Get("identity_state"))
	page, err := h.submissions.ListSubmissions(r.Context(), surveyport.ID(id), limit, offset, state)
	if err != nil {
		resultError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "questionnaire_id": id, "items": page.Items, "submissions": page.Items, "total": page.Total, "limit": page.Limit, "offset": page.Offset, "side_effect_executed": false})
}
func (h *Handler) submissionDetail(w http.ResponseWriter, r *http.Request, qid, sid int64) {
	if r.Method != http.MethodGet {
		method(w, "GET")
		return
	}
	if !h.read(w, r) {
		return
	}
	s, err := h.submissions.GetSubmission(r.Context(), surveyport.ID(sid))
	if err != nil || s.QuestionnaireID != surveyport.ID(qid) {
		resultError(w, surveyport.ErrNotFound)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, map[string]any{"ok": true, "submission": s})
}
func (h *Handler) export(w http.ResponseWriter, r *http.Request, id int64) {
	if r.Method != http.MethodGet {
		method(w, "GET")
		return
	}
	if _, ok := h.write(w, r); !ok {
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=questionnaire-%d.csv", id))
	w.Header().Set("Cache-Control", "no-store")
	writer := csv.NewWriter(w)
	_ = writer.Write([]string{"submission_id", "submitted_at", "identity_state", "customer_id", "total_score"})
	for offset := int32(0); ; {
		page, err := h.submissions.ListSubmissions(r.Context(), surveyport.ID(id), 100, offset, "")
		if err != nil {
			return
		}
		for _, s := range page.Items {
			customer := ""
			if s.Identity.CustomerID != nil {
				customer = fmt.Sprint(*s.Identity.CustomerID)
			}
			_ = writer.Write([]string{fmt.Sprint(s.ID), s.SubmittedAt.Format("2006-01-02T15:04:05Z07:00"), string(s.Identity.State), customer, fmt.Sprint(s.TotalScore)})
		}
		writer.Flush()
		if writer.Error() != nil || len(page.Items) == 0 || int64(offset)+int64(len(page.Items)) >= page.Total {
			break
		}
		offset += int32(len(page.Items))
	}
}
func (h *Handler) customerHistory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		method(w, "GET")
		return
	}
	if !h.read(w, r) {
		return
	}
	trim := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/v1/customers/"), "/survey-answers")
	id, err := parseID(strings.Trim(trim, "/"))
	if err != nil {
		writeError(w, 404, "not_found")
		return
	}
	limit, offset, ok := pageParams(r, 100)
	if !ok {
		writeError(w, 400, "invalid_request")
		return
	}
	page, err := h.submissions.CustomerHistory(r.Context(), id, limit, offset)
	if err != nil {
		resultError(w, err)
		return
	}
	writeJSON(w, 200, page)
}
func (h *Handler) unresolved(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		method(w, "GET")
		return
	}
	if !h.read(w, r) {
		return
	}
	limit, offset, ok := pageParams(r, 100)
	if !ok {
		writeError(w, 400, "invalid_request")
		return
	}
	var questionnaire surveyport.ID
	if raw := r.URL.Query().Get("questionnaire_id"); raw != "" {
		id, e := parseID(raw)
		if e != nil {
			writeError(w, 400, "invalid_request")
			return
		}
		questionnaire = surveyport.ID(id)
	}
	items, total, err := h.submissions.ListLegacyUnresolved(r.Context(), questionnaire, limit, offset)
	if err != nil {
		resultError(w, err)
		return
	}
	rows := make([]any, 0, len(items))
	for _, item := range items {
		rows = append(rows, legacySubmissionResponse(item))
	}
	writeJSON(w, 200, legacyHistoryEnvelope(map[string]any{"items": rows, "total": total, "limit": limit, "offset": offset}))
}

func (h *Handler) unresolvedTail(w http.ResponseWriter, r *http.Request, tail string) {
	parts := strings.Split(strings.Trim(tail, "/"), "/")
	id, err := parseID(parts[0])
	if err != nil {
		writeError(w, 404, "not_found")
		return
	}
	if r.Method != http.MethodGet {
		method(w, "GET")
		return
	}
	if !h.read(w, r) {
		return
	}
	if len(parts) == 1 {
		item, e := h.submissions.GetLegacyUnresolved(r.Context(), surveyport.ID(id))
		if e != nil {
			resultError(w, e)
			return
		}
		writeJSON(w, 200, legacyHistoryEnvelope(map[string]any{"item": legacySubmissionResponse(item)}))
		return
	}
	if len(parts) == 2 && parts[1] == "answers" {
		limit, offset, ok := pageParams(r, 100)
		if !ok {
			writeError(w, 400, "invalid_request")
			return
		}
		items, total, e := h.submissions.ListLegacyAnswers(r.Context(), surveyport.ID(id), limit, offset)
		if e != nil {
			resultError(w, e)
			return
		}
		rows := make([]any, 0, len(items))
		for _, item := range items {
			rows = append(rows, map[string]any{"id": item.ID, "source_id": item.SourceID, "submission_id": item.SubmissionID, "submission_source_id": item.SubmissionSourceID, "question_source_id": item.QuestionSourceID, "question_type": item.QuestionType, "question_title_snapshot": item.QuestionTitle, "selected_option_ids": item.SelectedOptionIDs, "selected_option_texts": item.SelectedOptionTexts, "selected_option_scores": item.SelectedOptionScores, "selected_option_tags": item.SelectedOptionTags, "text_value": item.TextValue, "score_contribution": item.ScoreContribution, "created_at": item.CreatedAt})
		}
		writeJSON(w, 200, legacyHistoryEnvelope(map[string]any{"items": rows, "total": total, "limit": limit, "offset": offset}))
		return
	}
	writeError(w, 404, "not_found")
}

func legacySubmissionResponse(item surveyport.LegacySubmission) map[string]any {
	return map[string]any{"id": item.ID, "source_id": item.SourceID, "questionnaire_source_id": item.QuestionnaireSourceID, "questionnaire_id": item.QuestionnaireID, "customer_id": item.CustomerID, "matched_by": item.MatchedBy, "source_channel": item.SourceChannel, "total_score": item.TotalScore, "final_tags": item.FinalTags, "submitted_at": item.SubmittedAt, "created_at": item.CreatedAt}
}
func legacyHistoryEnvelope(values map[string]any) map[string]any {
	out := map[string]any{"source": "v1_history", "read_only": true, "real_external_call_executed": false, "definition_mapping": "historical_source_only"}
	for key, value := range values {
		out[key] = value
	}
	return out
}
func (h *Handler) sidebar(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		method(w, "GET")
		return
	}
	if !h.read(w, r) {
		return
	}
	page, err := h.definitions.List(r.Context(), 50, 0, "", surveyport.StatusPublished)
	if err != nil {
		resultError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "items": page.Items})
}
func (h *Handler) preflight(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		method(w, "GET")
		return
	}
	if !h.read(w, r) {
		return
	}
	oauthEnabled := h.oauth != nil && h.oauth.Enabled()
	writeJSON(w, 200, map[string]any{"ok": true, "status": "ok", "checks": map[string]bool{"wechat_oauth_configured": oauthEnabled, "wecom_contact_configured": false, "debug_session_api_enabled": false, "wecom_tags_api_available": true, "questionnaire_admin_ui_enabled": true, "identity_map_available": true}})
}
func (h *Handler) operationsDisabled(w http.ResponseWriter, r *http.Request, id int64) {
	if r.Method == http.MethodGet {
		if !h.read(w, r) {
			return
		}
		config, err := h.submissions.GetOperationConfiguration(r.Context(), surveyport.ID(id))
		if err != nil {
			resultError(w, err)
			return
		}
		items, total, err := h.submissions.ListOperationReceipts(r.Context(), surveyport.ID(id), 100, 0)
		if err != nil {
			resultError(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"questionnaire_id": id, "completion": map[string]any{"navigation_target_id": config.CompletionNavigationRef, "channel_id": config.CompletionChannelID}, "external_push": map[string]any{"enabled": config.ExternalPushEnabled, "configuration_reference": config.ExternalPushConfigurationRef}, "configuration_version": config.Version, "provider_enabled": false, "local_only": true, "items": items, "total": total, "real_external_call_executed": false})
		return
	}
	principal, ok := h.write(w, r)
	if !ok {
		return
	}
	if r.Method == http.MethodPut && (strings.HasSuffix(r.URL.Path, "/operations/completion") || strings.HasSuffix(r.URL.Path, "/operations/external-push")) {
		config, err := h.submissions.GetOperationConfiguration(r.Context(), surveyport.ID(id))
		if err != nil {
			resultError(w, err)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/completion") {
			var body struct {
				NavigationTargetID string `json:"navigation_target_id"`
				ChannelID          *int64 `json:"channel_id"`
			}
			if decode(r, &body) != nil {
				writeError(w, 400, "invalid_request")
				return
			}
			config.CompletionNavigationRef, config.CompletionChannelID = body.NavigationTargetID, body.ChannelID
		} else {
			var body struct {
				Enabled                bool   `json:"enabled"`
				ConfigurationReference string `json:"configuration_reference"`
			}
			if decode(r, &body) != nil {
				writeError(w, 400, "invalid_request")
				return
			}
			config.ExternalPushEnabled, config.ExternalPushConfigurationRef = body.Enabled, body.ConfigurationReference
		}
		stored, err := h.submissions.SaveOperationConfiguration(r.Context(), config, principal.InternalID, idempotency(r))
		if err != nil {
			resultError(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"questionnaire_id": id, "completion": map[string]any{"navigation_target_id": stored.CompletionNavigationRef, "channel_id": stored.CompletionChannelID}, "external_push": map[string]any{"enabled": stored.ExternalPushEnabled, "configuration_reference": stored.ExternalPushConfigurationRef}, "configuration_version": stored.Version, "local_only": true, "provider_enabled": false, "real_external_call_executed": false})
		return
	}
	if r.Method == http.MethodPost && !strings.HasSuffix(r.URL.Path, "/reconcile") {
		var sid *surveyport.ID
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		for index, part := range parts {
			if part == "submissions" && index+1 < len(parts) {
				if parsed, err := parseID(parts[index+1]); err == nil {
					value := surveyport.ID(parsed)
					sid = &value
				}
			}
		}
		receipt, err := h.submissions.RecordDisabledOperation(r.Context(), surveyport.ID(id), sid, "external_push", principal.InternalID, idempotency(r))
		if err != nil {
			resultError(w, err)
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]any{"test_run_id": receipt.ID, "questionnaire_id": id, "submission_id": sid, "status": receipt.Status, "attempt_count": 0, "side_effect_executed": false, "real_external_call_executed": false, "provider_result_received": false, "unknown_after_dispatch": false, "auto_retry_allowed": false, "created_at": receipt.OccurredAt, "updated_at": receipt.OccurredAt})
		return
	}
	writeError(w, http.StatusServiceUnavailable, "provider_disabled")
}

func (h *Handler) legacyAdminTail(w http.ResponseWriter, r *http.Request, tail string) {
	parts := strings.Split(strings.Trim(tail, "/"), "/")
	if len(parts) == 1 && parts[0] == "external-push-logs" {
		h.legacyOperationLogs(w, r, 0)
		return
	}
	if len(parts) != 2 || parts[1] != "external-push-logs" {
		writeError(w, 404, "not_found")
		return
	}
	id, err := parseID(parts[0])
	if err != nil {
		writeError(w, 404, "not_found")
		return
	}
	h.legacyOperationLogs(w, r, surveyport.ID(id))
}

func (h *Handler) legacyOperationLogs(w http.ResponseWriter, r *http.Request, id surveyport.ID) {
	if r.Method != http.MethodGet {
		method(w, "GET")
		return
	}
	if !h.read(w, r) {
		return
	}
	limit, offset, ok := pageParams(r, 100)
	if !ok {
		writeError(w, 400, "invalid_request")
		return
	}
	items, total, err := h.submissions.ListOperationReceipts(r.Context(), id, limit, offset)
	if err != nil {
		resultError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"items": items, "total": total, "limit": limit, "offset": offset, "has_more": int64(offset)+int64(len(items)) < total, "local_only": true, "real_external_call_executed": false})
}

func definitionResponse(q surveyport.Questionnaire) map[string]any {
	return map[string]any{"id": q.ID, "name": q.Name, "title": q.Title, "description": q.Description, "answer_display_mode": q.AnswerDisplayMode, "assessment_enabled": q.Mode == surveyport.ModeAssessment, "assessment_config": json.RawMessage(q.AssessmentConfig), "slug": q.Slug, "is_disabled": q.Status == surveyport.StatusDisabled, "enabled": q.Status == surveyport.StatusPublished, "status": map[surveyport.QuestionnaireStatus]string{surveyport.StatusPublished: "active", surveyport.StatusDisabled: "disabled", surveyport.StatusDraft: "disabled"}[q.Status], "version": q.Version, "definition_version": q.DefinitionVersion, "question_count": len(q.Questions), "submission_count": 0, "created_at": q.CreatedAt, "updated_at": q.UpdatedAt, "public_path": "/h5/all.html?slug=" + q.Slug, "submitted_path": "/h5/result.html", "questions": q.Questions, "score_rules": q.ScoreRules}
}
func definitionEnvelope(q surveyport.Questionnaire, status string, source int64) map[string]any {
	value := definitionResponse(q)
	out := map[string]any{"ok": true, "questionnaire": value, "questions": q.Questions, "data": map[string]any{"questionnaire": value}, "questionnaire_id": q.ID}
	if status != "" {
		out["write_model_status"] = status
	}
	if source > 0 {
		out["source_questionnaire_id"] = source
	}
	return out
}
func publicDefinition(q surveyport.Questionnaire) map[string]any {
	questions := make([]map[string]any, 0, len(q.Questions))
	for _, question := range q.Questions {
		minimum, maximum := 0, len(question.Options)
		if question.Required {
			minimum = 1
		}
		if question.Type == surveyport.QuestionSingleChoice {
			maximum = 1
		}
		if question.Validation.MinimumSelections != nil {
			minimum = *question.Validation.MinimumSelections
		}
		if question.Validation.MaximumSelections != nil {
			maximum = *question.Validation.MaximumSelections
		}
		item := map[string]any{"id": question.ID, "type": question.Type, "title": question.Title, "required": question.Required, "sort_order": question.SortOrder, "minimum_selections": minimum, "maximum_selections": maximum, "placeholder_text": question.Placeholder, "assessment_dimension_key": question.AssessmentDimensionKey, "options": question.Options}
		if question.Validation.MinimumLength != nil {
			item["minimum_length"] = *question.Validation.MinimumLength
		}
		if question.Validation.MaximumLength != nil {
			item["maximum_length"] = *question.Validation.MaximumLength
		}
		questions = append(questions, item)
	}
	return map[string]any{"id": q.ID, "slug": q.Slug, "title": q.Title, "description": q.Description, "answer_display_mode": q.AnswerDisplayMode, "version": q.DefinitionVersion, "mode": q.Mode, "assessment_config": json.RawMessage(q.AssessmentConfig), "questions": questions}
}

func (h *Handler) read(w http.ResponseWriter, r *http.Request) bool {
	p, err := h.security.Authenticate(r.Context(), r)
	if err != nil {
		writeError(w, 401, "authentication_required")
		return false
	}
	return authorize(w, p, false)
}
func (h *Handler) write(w http.ResponseWriter, r *http.Request) (accessdomain.Principal, bool) {
	p, err := h.security.Authenticate(r.Context(), r)
	if err != nil {
		writeError(w, 401, "authentication_required")
		return p, false
	}
	if !authorize(w, p, true) {
		return p, false
	}
	if _, err = h.security.AuthorizeCSRF(r.Context(), r); err != nil {
		writeError(w, 403, "csrf_invalid")
		return p, false
	}
	return p, true
}
func authorize(w http.ResponseWriter, p accessdomain.Principal, write bool) bool {
	if p.InternalID < 1 || (p.Kind != accessdomain.KindAdmin && p.Kind != accessdomain.KindStaff) {
		writeError(w, 403, "permission_denied")
		return false
	}
	for _, role := range p.Roles {
		if !write && (role == accessdomain.RoleViewer || role == accessdomain.RoleAdmin || role == accessdomain.RoleSuperAdmin) || write && (role == accessdomain.RoleAdmin || role == accessdomain.RoleSuperAdmin) {
			return true
		}
	}
	writeError(w, 403, "permission_denied")
	return false
}
func decode(r *http.Request, value any) error {
	d := json.NewDecoder(io.LimitReader(r.Body, maxBody))
	d.DisallowUnknownFields()
	if err := d.Decode(value); err != nil {
		return err
	}
	if d.Decode(&struct{}{}) != io.EOF {
		return errors.New("trailing json")
	}
	return nil
}
func idempotency(r *http.Request) string {
	if v := r.Header.Get("Idempotency-Key"); len(v) >= 16 && len(v) <= 200 {
		return v
	}
	var raw [24]byte
	_, _ = rand.Read(raw[:])
	return "survey-compat-" + base64.RawURLEncoding.EncodeToString(raw[:])
}
func parseID(v string) (int64, error) {
	if v == "" || (len(v) > 1 && v[0] == '0') {
		return 0, errors.New("invalid")
	}
	id, err := strconv.ParseInt(v, 10, 64)
	if err != nil || id < 1 {
		return 0, errors.New("invalid")
	}
	return id, nil
}
func pageParams(r *http.Request, max int32) (int32, int32, bool) {
	limit := int64(50)
	offset := int64(0)
	var err error
	if raw := r.URL.Query().Get("limit"); raw != "" {
		limit, err = strconv.ParseInt(raw, 10, 32)
		if err != nil {
			return 0, 0, false
		}
	}
	if raw := r.URL.Query().Get("offset"); raw != "" {
		offset, err = strconv.ParseInt(raw, 10, 32)
		if err != nil {
			return 0, 0, false
		}
	}
	return int32(limit), int32(offset), limit >= 1 && limit <= int64(max) && offset >= 0 && offset <= 1_000_000
}
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func writeError(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, map[string]any{"ok": false, "code": code, "message": code})
}
func resultError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, surveyport.ErrInvalid):
		writeError(w, 400, "invalid_request")
	case errors.Is(err, surveyport.ErrNotFound):
		writeError(w, 404, "questionnaire_not_found")
	case errors.Is(err, surveyport.ErrConflict):
		writeError(w, 409, "definition_version_conflict")
	case errors.Is(err, surveyport.ErrReferenced):
		writeError(w, 409, "questionnaire_has_history")
	default:
		writeError(w, 503, "survey_unavailable")
	}
}
func method(w http.ResponseWriter, allow string) {
	w.Header().Set("Allow", allow)
	writeError(w, 405, "method_not_allowed")
}
