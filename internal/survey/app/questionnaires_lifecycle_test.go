package app

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"testing"
	"time"

	surveyport "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/port"
)

type lifecycleStore struct {
	Store
	source    surveyport.Questionnaire
	receipts  map[string]Receipt
	created   []surveyport.Questionnaire
	published int
}

func receiptMapKey(d [32]byte) string { return hex.EncodeToString(d[:]) }
func (s *lifecycleStore) Reserve(_ context.Context, in Reservation) (Receipt, bool, error) {
	if s.receipts == nil {
		s.receipts = map[string]Receipt{}
	}
	key := receiptMapKey(in.KeyDigest)
	if prior, ok := s.receipts[key]; ok {
		return prior, false, nil
	}
	receipt := Receipt{ID: int64(len(s.receipts) + 1), Operation: in.Operation, ActorScope: in.ActorScope, State: "reserved", KeyDigest: in.KeyDigest, PayloadDigest: in.PayloadDigest}
	s.receipts[key] = receipt
	return receipt, true, nil
}
func (s *lifecycleStore) Complete(_ context.Context, id int64, result json.RawMessage, _ time.Time) (Receipt, error) {
	for key, receipt := range s.receipts {
		if receipt.ID == id {
			receipt.State, receipt.Result = "completed", append(json.RawMessage(nil), result...)
			s.receipts[key] = receipt
			return receipt, nil
		}
	}
	return Receipt{}, surveyport.ErrNotFound
}
func (s *lifecycleStore) AppendAuditAndOutbox(context.Context, string, surveyport.ID, string, json.RawMessage, string, time.Time) error {
	return nil
}
func (s *lifecycleStore) Get(context.Context, surveyport.ID, bool) (surveyport.Questionnaire, error) {
	return s.source, nil
}
func (s *lifecycleStore) Create(_ context.Context, q surveyport.Questionnaire, _ int64, _ time.Time) (surveyport.Questionnaire, error) {
	q.ID = surveyport.ID(100 + len(s.created))
	s.created = append(s.created, q)
	return q, nil
}
func (s *lifecycleStore) Publish(_ context.Context, id surveyport.ID, expected, _ int64, _ time.Time) (surveyport.Questionnaire, error) {
	s.published++
	return surveyport.Questionnaire{ID: id, Version: expected + 1, Status: surveyport.StatusPublished}, nil
}
func (s *lifecycleStore) SetStatus(_ context.Context, id surveyport.ID, status surveyport.QuestionnaireStatus, expected, _ int64, _ time.Time) (surveyport.Questionnaire, error) {
	return surveyport.Questionnaire{ID: id, Version: expected + 1, Status: status}, nil
}

func TestQuestionnaireLifecycleCreatesDuplicatesAndPublishesIdempotently(t *testing.T) {
	question := surveyport.Question{Type: surveyport.QuestionTextarea, Title: "需求", Required: true, SortOrder: 0}
	store := &lifecycleStore{source: surveyport.Questionnaire{ID: 7, Name: "增长问卷", Title: "增长问卷", Slug: "growth", Status: surveyport.StatusPublished, Mode: surveyport.ModeSurvey, AnswerDisplayMode: surveyport.DisplayAllInOne, Questions: []surveyport.Question{question}}}
	service := NewService(oauthUOW{}, store)
	service.now = func() time.Time { return time.Date(2026, 9, 5, 1, 2, 3, 0, time.UTC) }
	created, err := service.Create(context.Background(), surveyport.CreateCommand{Questionnaire: surveyport.Questionnaire{Name: "新问卷", Title: "新问卷", Slug: "new-growth", Status: surveyport.StatusDraft, Mode: surveyport.ModeSurvey, AnswerDisplayMode: surveyport.DisplayAllInOne, Questions: []surveyport.Question{question}}, ActorID: 3, IdempotencyKey: "questionnaire-create-lifecycle-0001"})
	if err != nil || created.ID != 100 {
		t.Fatalf("create=%+v err=%v", created, err)
	}
	copy, err := service.Duplicate(context.Background(), 7, 3, "questionnaire-duplicate-lifecycle-0002")
	if err != nil || copy.ID != 101 || copy.Status != surveyport.StatusDraft || copy.Slug != "growth-copy-1788570123" || copy.Questions[0].ID != 0 {
		t.Fatalf("duplicate=%+v err=%v", copy, err)
	}
	replay, err := service.Duplicate(context.Background(), 7, 3, "questionnaire-duplicate-lifecycle-0002")
	if err != nil || replay.ID != copy.ID || replay.Slug != copy.Slug || len(store.created) != 2 {
		t.Fatalf("duplicate replay=%+v err=%v creates=%d", replay, err, len(store.created))
	}
	published, err := service.Publish(context.Background(), 7, 1, 3, "questionnaire-publish-lifecycle-0003")
	if err != nil || published.Status != surveyport.StatusPublished || store.published != 1 {
		t.Fatalf("publish=%+v err=%v calls=%d", published, err, store.published)
	}
}
