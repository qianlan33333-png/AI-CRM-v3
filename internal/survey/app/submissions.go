package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	surveydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/domain"
	surveyport "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/port"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/survey/secure"
)

type PersistSubmission struct {
	Questionnaire                                   surveyport.Questionnaire
	Command                                         surveyport.SubmitCommand
	SubmissionKeyDigest, PayloadDigest, TokenDigest [32]byte
	Token                                           string
	TotalScore                                      float64
	Result                                          surveyport.AssessmentResult
	Answers                                         []surveyport.AnswerSnapshot
	Now                                             time.Time
}

type SubmissionStore interface {
	GetPublishedBySlug(context.Context, string) (surveyport.Questionnaire, error)
	CreateSubmission(context.Context, PersistSubmission) (surveyport.Submission, bool, error)
	GetSubmissionByTokenDigest(context.Context, [32]byte) (surveyport.Submission, error)
	ListSubmissions(context.Context, surveyport.ID, int32, int32, surveyport.IdentityState) ([]surveyport.Submission, int64, error)
	GetSubmission(context.Context, surveyport.ID) (surveyport.Submission, error)
	CustomerHistory(context.Context, int64, int32, int32) ([]surveyport.Submission, int64, error)
	SubmissionAnalytics(context.Context, surveyport.ID) (surveyport.Analytics, error)
	ListOperationReceipts(context.Context, surveyport.ID, int32, int32) ([]surveyport.OperationReceipt, int64, error)
	ListLegacyUnresolved(context.Context, surveyport.ID, int32, int32) ([]surveyport.LegacySubmission, int64, error)
	GetLegacyUnresolved(context.Context, surveyport.ID) (surveyport.LegacySubmission, error)
	ListLegacyAnswers(context.Context, surveyport.ID, int32, int32) ([]surveyport.LegacyAnswer, int64, error)
	GetOperationConfiguration(context.Context, surveyport.ID) (surveyport.OperationConfiguration, error)
	SaveOperationConfiguration(context.Context, surveyport.OperationConfiguration, int64, time.Time) (surveyport.OperationConfiguration, error)
	RecordDisabledOperation(context.Context, surveyport.ID, *surveyport.ID, string, [32]byte, time.Time) (surveyport.OperationReceipt, error)
	AppendAuditAndOutbox(context.Context, string, surveyport.ID, string, json.RawMessage, string, time.Time) error
}

type SubmissionService struct {
	uow    platformport.UnitOfWork
	store  SubmissionStore
	cipher *secure.Cipher
	now    func() time.Time
}

func NewSubmissionService(uow platformport.UnitOfWork, store SubmissionStore, cipher *secure.Cipher) *SubmissionService {
	return &SubmissionService{uow: uow, store: store, cipher: cipher, now: time.Now}
}

func (s *SubmissionService) ReadPublic(ctx context.Context, slug string) (surveyport.Questionnaire, error) {
	if s == nil || s.uow == nil || s.store == nil || !validSlug(slug) {
		return surveyport.Questionnaire{}, surveyport.ErrNotFound
	}
	var result surveyport.Questionnaire
	err := s.uow.Within(ctx, func(tx context.Context) error {
		var e error
		result, e = s.store.GetPublishedBySlug(tx, slug)
		return e
	})
	return result, classify(err)
}

func (s *SubmissionService) Submit(ctx context.Context, command surveyport.SubmitCommand) (surveyport.SubmissionReceipt, error) {
	if s == nil || s.uow == nil || s.store == nil || s.cipher == nil || !validSlug(command.Slug) || command.DefinitionVersion < 1 || !validPublicKey(command.SubmissionKey) || !validIdentity(command.Identity) || len(command.SourceChannel) > 100 || len(command.CampaignID) > 200 || len(command.StaffID) > 200 {
		return surveyport.SubmissionReceipt{}, surveyport.ErrInvalid
	}
	canonical, _ := json.Marshal(struct {
		Version       int64                         `json:"version"`
		Answers       []surveyport.SubmissionAnswer `json:"answers"`
		IdentityState surveyport.IdentityState      `json:"identity_state"`
		Evidence      string                        `json:"evidence_digest,omitempty"`
	}{command.DefinitionVersion, command.Answers, command.Identity.State, command.Identity.EvidenceDigest})
	payloadDigest := sha256.Sum256(canonical)
	submissionKeyDigest := sha256.Sum256([]byte(command.SubmissionKey))
	token, err := s.cipher.Token(command.Slug, command.SubmissionKey)
	if err != nil {
		return surveyport.SubmissionReceipt{}, surveyport.ErrUnavailable
	}
	tokenDigest := sha256.Sum256([]byte(token))
	now := s.now().UTC()
	var stored surveyport.Submission
	err = s.uow.Within(ctx, func(tx context.Context) error {
		questionnaire, e := s.store.GetPublishedBySlug(tx, command.Slug)
		if e != nil {
			return e
		}
		if questionnaire.DefinitionVersion != command.DefinitionVersion {
			return surveyport.ErrConflict
		}
		if e = surveydomain.ValidateAnswers(questionnaire.Questions, command.Answers); e != nil {
			return surveyport.ErrInvalid
		}
		answers, total, e := snapshotAnswers(questionnaire, command.Answers)
		if e != nil {
			return surveyport.ErrInvalid
		}
		result := surveyport.AssessmentResult{Dimensions: []surveyport.AssessmentDimensionResult{}, StrengthDimensionKeys: []string{}, WeaknessDimensionKeys: []string{}, TagCodes: []string{}}
		if questionnaire.Mode == surveyport.ModeAssessment {
			result, e = surveydomain.EvaluateAssessment(questionnaire, command.Answers)
			if e != nil {
				return surveyport.ErrInvalid
			}
			total = result.TotalScore
		}
		var created bool
		stored, created, e = s.store.CreateSubmission(tx, PersistSubmission{Questionnaire: questionnaire, Command: command, SubmissionKeyDigest: submissionKeyDigest, PayloadDigest: payloadDigest, TokenDigest: tokenDigest, Token: token, TotalScore: total, Result: result, Answers: answers, Now: now})
		if e != nil {
			return e
		}
		if !created {
			return nil
		}
		payload, _ := json.Marshal(map[string]any{"questionnaire_id": questionnaire.ID, "submission_id": stored.ID, "identity_state": command.Identity.State})
		outboxKey := "survey.submission:" + hex.EncodeToString(submissionKeyDigest[:])
		return s.store.AppendAuditAndOutbox(tx, "submission_created", questionnaire.ID, "public", payload, outboxKey, now)
	})
	if err != nil {
		return surveyport.SubmissionReceipt{}, classify(err)
	}
	return surveyport.SubmissionReceipt{QuestionnaireID: stored.QuestionnaireID, QuestionnaireSlug: stored.QuestionnaireSlug, DefinitionVersion: stored.DefinitionVersion, SubmissionID: stored.ID, ResultToken: token}, nil
}

func snapshotAnswers(q surveyport.Questionnaire, values []surveyport.SubmissionAnswer) ([]surveyport.AnswerSnapshot, float64, error) {
	byID := map[surveyport.ID]surveyport.SubmissionAnswer{}
	for _, a := range values {
		byID[a.QuestionID] = a
	}
	out := make([]surveyport.AnswerSnapshot, 0, len(values))
	total := 0.0
	for _, question := range q.Questions {
		answer, ok := byID[question.ID]
		if !ok {
			continue
		}
		qid := question.ID
		snapshot := surveyport.AnswerSnapshot{QuestionID: &qid, QuestionType: question.Type, QuestionTitle: question.Title, SortOrder: question.SortOrder, SelectedOptions: []surveyport.SelectedOptionSnapshot{}, TextValue: answer.TextValue}
		options := map[surveyport.ID]surveyport.Option{}
		for _, o := range question.Options {
			options[o.ID] = o
		}
		for _, id := range answer.OptionIDs {
			o, exists := options[id]
			if !exists {
				return nil, 0, surveyport.ErrInvalid
			}
			snapshot.SelectedOptions = append(snapshot.SelectedOptions, surveyport.SelectedOptionSnapshot{OptionID: id, OptionText: o.Text, Score: o.Score, TagCodes: append([]string(nil), o.TagCodes...)})
			snapshot.Score += o.Score
			total += o.Score
		}
		if question.Type == surveyport.QuestionMobile {
			snapshot.TextValueMasked = maskMobile(answer.TextValue)
		} else if answer.TextValue != "" {
			snapshot.TextValueMasked = "[protected]"
		}
		out = append(out, snapshot)
	}
	return out, total, nil
}

func (s *SubmissionService) QueryResult(ctx context.Context, token string) (surveyport.Submission, error) {
	if s == nil || s.uow == nil || s.store == nil || !validPublicKey(token) {
		return surveyport.Submission{}, surveyport.ErrNotFound
	}
	digest := sha256.Sum256([]byte(token))
	var result surveyport.Submission
	err := s.uow.Within(ctx, func(tx context.Context) error {
		var e error
		result, e = s.store.GetSubmissionByTokenDigest(tx, digest)
		return e
	})
	return result, classify(err)
}
func (s *SubmissionService) ListSubmissions(ctx context.Context, id surveyport.ID, limit, offset int32, state surveyport.IdentityState) (surveyport.SubmissionPage, error) {
	if limit == 0 {
		limit = DefaultLimit
	}
	if s == nil || s.uow == nil || s.store == nil || id < 1 || limit < 1 || limit > 100 || offset < 0 || offset > MaximumOffset || state != "" && !validIdentityState(state) {
		return surveyport.SubmissionPage{}, surveyport.ErrInvalid
	}
	page := surveyport.SubmissionPage{Limit: limit, Offset: offset}
	err := s.uow.Within(ctx, func(tx context.Context) error {
		var e error
		page.Items, page.Total, e = s.store.ListSubmissions(tx, id, limit, offset, state)
		return e
	})
	return page, classify(err)
}
func (s *SubmissionService) GetSubmission(ctx context.Context, id surveyport.ID) (surveyport.Submission, error) {
	if s == nil || s.uow == nil || s.store == nil || id < 1 {
		return surveyport.Submission{}, surveyport.ErrNotFound
	}
	var result surveyport.Submission
	err := s.uow.Within(ctx, func(tx context.Context) error { var e error; result, e = s.store.GetSubmission(tx, id); return e })
	return result, classify(err)
}
func (s *SubmissionService) CustomerHistory(ctx context.Context, customer int64, limit, offset int32) (surveyport.SubmissionPage, error) {
	if limit == 0 {
		limit = DefaultLimit
	}
	if s == nil || s.uow == nil || s.store == nil || customer < 1 || limit < 1 || limit > 100 || offset < 0 || offset > MaximumOffset {
		return surveyport.SubmissionPage{}, surveyport.ErrInvalid
	}
	page := surveyport.SubmissionPage{Limit: limit, Offset: offset}
	err := s.uow.Within(ctx, func(tx context.Context) error {
		var e error
		page.Items, page.Total, e = s.store.CustomerHistory(tx, customer, limit, offset)
		return e
	})
	return page, classify(err)
}
func (s *SubmissionService) Analytics(ctx context.Context, id surveyport.ID) (surveyport.Analytics, error) {
	if s == nil || s.uow == nil || s.store == nil || id < 1 {
		return surveyport.Analytics{}, surveyport.ErrInvalid
	}
	var result surveyport.Analytics
	err := s.uow.Within(ctx, func(tx context.Context) error { var e error; result, e = s.store.SubmissionAnalytics(tx, id); return e })
	return result, classify(err)
}

func (s *SubmissionService) ListOperationReceipts(ctx context.Context, id surveyport.ID, limit, offset int32) ([]surveyport.OperationReceipt, int64, error) {
	if s == nil || s.uow == nil || s.store == nil || id < 0 || limit < 1 || limit > 100 || offset < 0 || offset > MaximumOffset {
		return nil, 0, surveyport.ErrInvalid
	}
	var items []surveyport.OperationReceipt
	var total int64
	err := s.uow.Within(ctx, func(tx context.Context) error {
		var e error
		items, total, e = s.store.ListOperationReceipts(tx, id, limit, offset)
		return e
	})
	return items, total, classify(err)
}

func (s *SubmissionService) ListLegacyUnresolved(ctx context.Context, questionnaire surveyport.ID, limit, offset int32) ([]surveyport.LegacySubmission, int64, error) {
	if s == nil || s.uow == nil || s.store == nil || questionnaire < 0 || limit < 1 || limit > 100 || offset < 0 || offset > MaximumOffset {
		return nil, 0, surveyport.ErrInvalid
	}
	var items []surveyport.LegacySubmission
	var total int64
	err := s.uow.Within(ctx, func(tx context.Context) error {
		var e error
		items, total, e = s.store.ListLegacyUnresolved(tx, questionnaire, limit, offset)
		return e
	})
	return items, total, classify(err)
}
func (s *SubmissionService) GetLegacyUnresolved(ctx context.Context, id surveyport.ID) (surveyport.LegacySubmission, error) {
	if s == nil || s.uow == nil || s.store == nil || id < 1 {
		return surveyport.LegacySubmission{}, surveyport.ErrNotFound
	}
	var item surveyport.LegacySubmission
	err := s.uow.Within(ctx, func(tx context.Context) error { var e error; item, e = s.store.GetLegacyUnresolved(tx, id); return e })
	return item, classify(err)
}
func (s *SubmissionService) ListLegacyAnswers(ctx context.Context, id surveyport.ID, limit, offset int32) ([]surveyport.LegacyAnswer, int64, error) {
	if s == nil || s.uow == nil || s.store == nil || id < 1 || limit < 1 || limit > 100 || offset < 0 || offset > MaximumOffset {
		return nil, 0, surveyport.ErrInvalid
	}
	var items []surveyport.LegacyAnswer
	var total int64
	err := s.uow.Within(ctx, func(tx context.Context) error {
		var e error
		items, total, e = s.store.ListLegacyAnswers(tx, id, limit, offset)
		return e
	})
	return items, total, classify(err)
}

func (s *SubmissionService) GetOperationConfiguration(ctx context.Context, id surveyport.ID) (surveyport.OperationConfiguration, error) {
	if s == nil || s.uow == nil || s.store == nil || id < 1 {
		return surveyport.OperationConfiguration{}, surveyport.ErrNotFound
	}
	var value surveyport.OperationConfiguration
	err := s.uow.Within(ctx, func(tx context.Context) error {
		var e error
		value, e = s.store.GetOperationConfiguration(tx, id)
		return e
	})
	return value, classify(err)
}
func (s *SubmissionService) SaveOperationConfiguration(ctx context.Context, value surveyport.OperationConfiguration, actor int64, key string) (surveyport.OperationConfiguration, error) {
	if s == nil || s.uow == nil || s.store == nil || value.QuestionnaireID < 1 || actor < 1 || !validPublicKeyish(key) || !validOpaque(value.CompletionNavigationRef) || !validOpaque(value.ExternalPushConfigurationRef) || value.CompletionChannelID != nil && *value.CompletionChannelID < 1 || value.ExternalPushEnabled && value.ExternalPushConfigurationRef == "" {
		return surveyport.OperationConfiguration{}, surveyport.ErrInvalid
	}
	now := s.now().UTC()
	var stored surveyport.OperationConfiguration
	err := s.uow.Within(ctx, func(tx context.Context) error {
		var e error
		stored, e = s.store.SaveOperationConfiguration(tx, value, actor, now)
		if e != nil {
			return e
		}
		payload, _ := json.Marshal(map[string]any{"questionnaire_id": value.QuestionnaireID, "external_push_enabled": value.ExternalPushEnabled, "configuration_reference": value.ExternalPushConfigurationRef, "completion_configured": value.CompletionNavigationRef != "" || value.CompletionChannelID != nil})
		return s.store.AppendAuditAndOutbox(tx, "survey_operation_configuration_saved", value.QuestionnaireID, fmt.Sprint(actor), payload, "survey-operation-config:"+key, now)
	})
	return stored, classify(err)
}
func (s *SubmissionService) RecordDisabledOperation(ctx context.Context, qid surveyport.ID, sid *surveyport.ID, kind string, actor int64, key string) (surveyport.OperationReceipt, error) {
	if s == nil || s.uow == nil || s.store == nil || qid < 1 || actor < 1 || !validPublicKeyish(key) || kind != "external_push" && kind != "completion" {
		return surveyport.OperationReceipt{}, surveyport.ErrInvalid
	}
	digest := sha256.Sum256([]byte(key))
	now := s.now().UTC()
	var receipt surveyport.OperationReceipt
	err := s.uow.Within(ctx, func(tx context.Context) error {
		var e error
		receipt, e = s.store.RecordDisabledOperation(tx, qid, sid, kind, digest, now)
		if e != nil {
			return e
		}
		payload, _ := json.Marshal(map[string]any{"questionnaire_id": qid, "operation_receipt_id": receipt.ID, "status": "disabled"})
		return s.store.AppendAuditAndOutbox(tx, "survey_external_operation_disabled", qid, fmt.Sprint(actor), payload, "survey-disabled:"+key, now)
	})
	return receipt, classify(err)
}

func validOpaque(v string) bool {
	if len(v) > 128 {
		return false
	}
	for _, r := range v {
		if !(r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || strings.ContainsRune("._:-", r)) {
			return false
		}
	}
	return true
}
func validPublicKeyish(v string) bool { return len(v) >= 16 && len(v) <= 200 }

func validSlug(v string) bool {
	return len(v) > 0 && len(v) <= 128 && strings.Trim(v, "abcdefghijklmnopqrstuvwxyz0123456789-") == "" && v[0] != '-' && v[len(v)-1] != '-'
}
func validPublicKey(v string) bool {
	if len(v) != 43 {
		return false
	}
	for _, r := range v {
		if !(r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
			return false
		}
	}
	return true
}
func validIdentity(v surveyport.SubmissionIdentity) bool {
	if !validIdentityState(v.State) {
		return false
	}
	if v.State == surveyport.IdentityResolved {
		return v.CustomerID != nil && *v.CustomerID > 0 && len(v.EvidenceDigest) == 64
	}
	return v.CustomerID == nil && len(v.EvidenceDigest) <= 64
}
func validIdentityState(v surveyport.IdentityState) bool {
	return v == surveyport.IdentityAnonymous || v == surveyport.IdentityResolved || v == surveyport.IdentityUnresolved || v == surveyport.IdentityConflict
}
func maskMobile(v string) string {
	r := []rune(v)
	if len(r) < 7 {
		return "***"
	}
	return string(r[:3]) + "****" + string(r[len(r)-4:])
}

var _ surveyport.PublicApplication = (*SubmissionService)(nil)
var _ surveyport.SubmissionApplication = (*SubmissionService)(nil)
