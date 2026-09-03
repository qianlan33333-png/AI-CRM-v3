package main

import (
	"context"
	"errors"
	"testing"
	"time"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
	surveyport "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/port"
	tagport "github.com/qianlan33333-png/AI-CRM-v3/internal/tag/port"
	wecomport "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
)

type profileTestUOW struct{}

func (profileTestUOW) Within(ctx context.Context, run func(context.Context) error) error {
	return run(ctx)
}

type profileObservations struct {
	owners []wecomport.OwnerObservation
	tags   []wecomport.TagObservation
}

func (value profileObservations) CustomerOwnerObservations(context.Context, customerdomain.CustomerID) ([]wecomport.OwnerObservation, error) {
	return value.owners, nil
}
func (value profileObservations) CustomerTagObservations(context.Context, customerdomain.CustomerID) ([]wecomport.TagObservation, error) {
	return value.tags, nil
}

type profileUsers map[string]string

func (users profileUsers) UserByWeComUserID(_ context.Context, id string, _ bool) (accessdomain.User, error) {
	name, ok := users[id]
	if !ok {
		return accessdomain.User{}, accessdomain.ErrNotFound
	}
	return accessdomain.User{DisplayName: name}, nil
}

type profileTagNames map[string]tagport.ProviderTagName

func (names profileTagNames) ProviderTagNames(_ context.Context, ids []string) ([]tagport.ProviderTagName, error) {
	items := []tagport.ProviderTagName{}
	for _, id := range ids {
		if item, ok := names[id]; ok {
			items = append(items, item)
		}
	}
	return items, nil
}

type profileSurveyReader struct{ item surveyport.Submission }

func (reader profileSurveyReader) CustomerHistoryWindow(context.Context, surveyport.CustomerHistoryQuery) (surveyport.CustomerHistoryWindow, error) {
	return surveyport.CustomerHistoryWindow{Items: []surveyport.Submission{reader.item}}, nil
}

func TestCustomerOwnerAndTagAdaptersNeverExposeProviderIDs(t *testing.T) {
	now := time.Date(2026, 9, 3, 10, 0, 0, 0, time.UTC)
	observations := profileObservations{
		owners: []wecomport.OwnerObservation{{EmployeeID: "raw-userid-mapped", Status: "active", ObservedAt: now}, {EmployeeID: "raw-userid-unmatched", Status: "active", ObservedAt: now}},
		tags:   []wecomport.TagObservation{{ProviderTagID: "raw-provider-tag", ProviderType: 1, Status: "active", ObservedAt: now}},
	}
	owners, err := (customerOwnerAdapter{uow: profileTestUOW{}, observations: observations, users: profileUsers{"raw-userid-mapped": "小王"}}).CustomerOwners(context.Background(), 42)
	if err != nil || len(owners.Items) != 1 || owners.Items[0].DisplayName != "小王" || owners.UnmatchedCount != 1 || owners.Status.State != customerport.SectionDegraded {
		t.Fatalf("owners=%+v err=%v", owners, err)
	}
	tags, err := (customerTagAdapter{uow: profileTestUOW{}, observations: observations, names: profileTagNames{}}).CustomerTags(context.Background(), 42)
	if err != nil || len(tags.Items) != 1 || tags.Items[0].Name != "标签名称待同步" || tags.Items[0].Name == "raw-provider-tag" || tags.Status.State != customerport.SectionDegraded {
		t.Fatalf("tags=%+v err=%v", tags, err)
	}
}

func TestCustomerSurveyAdapterUsesOnlySurveyMaskedAnswers(t *testing.T) {
	now := time.Date(2026, 9, 3, 10, 0, 0, 0, time.UTC)
	adapter := customerSurveyAdapter{reader: profileSurveyReader{item: surveyport.Submission{ID: 8, QuestionnaireTitle: "安全问卷", SubmittedAt: now,
		Answers: []surveyport.AnswerSnapshot{{QuestionTitle: "手机号", TextValue: "13812345678", TextValueMasked: "138****5678"},
			{QuestionTitle: "选择", SelectedOptions: []surveyport.SelectedOptionSnapshot{{OptionText: "选项 A"}}}}}}}
	page, err := adapter.CustomerSurveys(context.Background(), 42, customerport.PageQuery{Limit: 21, Watermark: now})
	if err != nil || len(page.Items) != 1 || page.Items[0].Answers[0].Answers[0] != "138****5678" || page.Items[0].Answers[1].Answers[0] != "选项 A" {
		t.Fatalf("page=%+v err=%v", page, err)
	}
	if page.Items[0].Answers[0].Answers[0] == "13812345678" {
		t.Fatal("raw survey text leaked")
	}
}

func TestCustomerSectionAdapterClassifiesSourceFailure(t *testing.T) {
	failing := failingProfileObservations{}
	_, err := (customerOwnerAdapter{uow: profileTestUOW{}, observations: failing, users: profileUsers{}}).CustomerOwners(context.Background(), 42)
	if !errors.Is(err, customerport.ErrSectionUnavailable) {
		t.Fatalf("err=%v", err)
	}
}

type failingProfileObservations struct{}

func (failingProfileObservations) CustomerOwnerObservations(context.Context, customerdomain.CustomerID) ([]wecomport.OwnerObservation, error) {
	return nil, errors.New("database unavailable")
}
func (failingProfileObservations) CustomerTagObservations(context.Context, customerdomain.CustomerID) ([]wecomport.TagObservation, error) {
	return nil, errors.New("database unavailable")
}
