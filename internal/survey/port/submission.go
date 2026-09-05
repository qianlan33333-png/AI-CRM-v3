package port

import (
	"context"
	"encoding/json"
	"time"
)

type SubmitCommand struct {
	Slug                               string
	DefinitionVersion                  int64
	SubmissionKey                      string
	Answers                            []SubmissionAnswer
	Identity                           SubmissionIdentity
	SourceChannel, CampaignID, StaffID string
}

type SubmissionReceipt struct {
	QuestionnaireID   ID     `json:"questionnaire_id"`
	QuestionnaireSlug string `json:"questionnaire_slug"`
	DefinitionVersion int64  `json:"definition_version"`
	SubmissionID      ID     `json:"submission_id"`
	ResultToken       string `json:"result_token,omitempty"`
}

type AnswerSnapshot struct {
	ID                      ID                       `json:"id"`
	QuestionID              *ID                      `json:"question_id,omitempty"`
	LegacySourceQuestionID  *int64                   `json:"legacy_source_question_id,omitempty"`
	QuestionType            QuestionType             `json:"question_type"`
	QuestionTitle           string                   `json:"question_title"`
	SortOrder               int                      `json:"sort_order"`
	SelectedOptions         []SelectedOptionSnapshot `json:"selected_options"`
	TextValue               string                   `json:"text_value,omitempty"`
	TextValueMasked         string                   `json:"text_value_masked,omitempty"`
	Score                   float64                  `json:"score"`
	LegacyDefinitionMissing bool                     `json:"legacy_definition_missing"`
	PhoneBindingStatus      string                   `json:"phone_binding_status,omitempty"`
}

type SelectedOptionSnapshot struct {
	OptionID   ID       `json:"option_id"`
	OptionText string   `json:"option_text"`
	Score      float64  `json:"score"`
	TagCodes   []string `json:"tag_codes,omitempty"`
}

type Submission struct {
	ID                                 ID                 `json:"id"`
	QuestionnaireID                    ID                 `json:"questionnaire_id"`
	DefinitionVersion                  int64              `json:"definition_version"`
	QuestionnaireSlug                  string             `json:"questionnaire_slug"`
	QuestionnaireTitle                 string             `json:"questionnaire_title"`
	Mode                               QuestionnaireMode  `json:"mode"`
	Identity                           SubmissionIdentity `json:"identity"`
	TotalScore                         float64            `json:"total_score"`
	Result                             AssessmentResult   `json:"assessment_result"`
	SourceChannel, CampaignID, StaffID string
	SubmittedAt                        time.Time        `json:"submitted_at"`
	Answers                            []AnswerSnapshot `json:"answers"`
}

type SubmissionPage struct {
	Items         []Submission `json:"items"`
	Total         int64        `json:"total"`
	Limit, Offset int32
}

type CustomerHistoryQuery struct {
	CustomerID int64
	Limit      int32
	Watermark  time.Time
	AfterAt    time.Time
	AfterID    ID
}

type CustomerHistoryWindow struct {
	Items []Submission
}

// CustomerHistoryReader is the stable, customer_id-only read boundary used by
// the Customer profile composition adapter. Returned free text is already
// masked by Survey and must never be rehydrated by consumers.
type CustomerHistoryReader interface {
	CustomerHistoryWindow(context.Context, CustomerHistoryQuery) (CustomerHistoryWindow, error)
}

type Analytics struct {
	QuestionnaireID   ID                  `json:"questionnaire_id"`
	DefinitionVersion int64               `json:"definition_version"`
	Slug              string              `json:"slug"`
	State             QuestionnaireStatus `json:"state"`
	SubmissionCount   int64               `json:"submission_count"`
	AverageScore      float64             `json:"average_score"`
}

type OperationReceipt struct {
	ID                 ID        `json:"id"`
	QuestionnaireID    ID        `json:"questionnaire_id"`
	SubmissionID       *ID       `json:"submission_id,omitempty"`
	OperationKind      string    `json:"operation_kind"`
	Status             string    `json:"status"`
	FailureCategory    string    `json:"failure_category,omitempty"`
	OccurrenceCount    int64     `json:"occurrence_count"`
	OccurredAt         time.Time `json:"occurred_at"`
	ReadOnlyLegacy     bool      `json:"read_only_legacy"`
	Replayable         bool      `json:"replayable"`
	RealEffectExecuted bool      `json:"real_external_call_executed"`
}

type OperationConfiguration struct {
	QuestionnaireID              ID              `json:"-"`
	CompletionNavigationRef      string          `json:"navigation_target_id,omitempty"`
	CompletionChannelID          *int64          `json:"channel_id,omitempty"`
	ExternalPushEnabled          bool            `json:"external_push_enabled"`
	ExternalPushConfigurationRef string          `json:"configuration_reference,omitempty"`
	ExternalPushMetadata         json.RawMessage `json:"metadata,omitempty"`
	Version                      int64           `json:"version"`
	UpdatedAt                    time.Time       `json:"updated_at,omitempty"`
}

type LegacySubmission struct {
	ID, SourceID, QuestionnaireSourceID int64
	QuestionnaireID, CustomerID         *int64
	MatchedBy, SourceChannel            string
	TotalScore                          float64
	FinalTags                           json.RawMessage
	SubmittedAt, CreatedAt              time.Time
}

type LegacyAnswer struct {
	ID, SourceID, SubmissionID, SubmissionSourceID, QuestionSourceID                 int64
	QuestionType, QuestionTitle, TextValue                                           string
	SelectedOptionIDs, SelectedOptionTexts, SelectedOptionScores, SelectedOptionTags json.RawMessage
	ScoreContribution                                                                float64
	CreatedAt                                                                        time.Time
}

type PublicApplication interface {
	ReadPublic(context.Context, string) (Questionnaire, error)
	Submit(context.Context, SubmitCommand) (SubmissionReceipt, error)
	QueryResult(context.Context, string) (Submission, error)
}

type SubmissionApplication interface {
	ListSubmissions(context.Context, ID, int32, int32, IdentityState) (SubmissionPage, error)
	GetSubmission(context.Context, ID) (Submission, error)
	CustomerHistory(context.Context, int64, int32, int32) (SubmissionPage, error)
	Analytics(context.Context, ID) (Analytics, error)
	RecordExport(context.Context, ID, int64, string) error
	ListOperationReceipts(context.Context, ID, int32, int32) ([]OperationReceipt, int64, error)
	ListLegacyUnresolved(context.Context, ID, int32, int32) ([]LegacySubmission, int64, error)
	GetLegacyUnresolved(context.Context, ID) (LegacySubmission, error)
	ListLegacyAnswers(context.Context, ID, int32, int32) ([]LegacyAnswer, int64, error)
	GetOperationConfiguration(context.Context, ID) (OperationConfiguration, error)
	SaveOperationConfiguration(context.Context, OperationConfiguration, int64, string) (OperationConfiguration, error)
	RecordDisabledOperation(context.Context, ID, *ID, string, int64, string) (OperationReceipt, error)
	QueueCompletionTest(context.Context, ID, int64, string) (CompletionTestReceipt, error)
}

type MigrationRecord struct {
	SourceSystem, SourceTable, SourcePK string
	RecordDigest                        [32]byte
	SafeSnapshot                        json.RawMessage
}
