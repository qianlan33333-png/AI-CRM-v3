package excel

import (
	"context"
	ai "github.com/qianlan33333-png/AI-CRM-v3/internal/aiassistant/port"
	outbound "github.com/qianlan33333-png/AI-CRM-v3/internal/outbound/port"
	"testing"
)

func TestMissingTitleOrCoverNeverLoadsMedia(t *testing.T) {
	// An unusable URL proves validation occurs before any network request.
	client := &Client{Base: "invalid"}
	for _, test := range []struct{ title, cover, code string }{
		{"", "", "title_missing"}, {"  ", "", "title_missing"}, {"Excel title", "", "cover_missing"},
	} {
		_, err := client.LoadExcelCard(context.Background(), ai.ExcelCard{AppID: "app", Path: "pages/a/a", Title: test.title})
		coded, ok := err.(outbound.PayloadPreparationError)
		if !ok || coded.FailureCode() != test.code {
			t.Fatalf("missing content did not fail safely: %v", err)
		}
	}
}
