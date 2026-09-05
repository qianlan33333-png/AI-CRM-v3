package main

import (
 "net/http/httptest"
 "testing"
)

// surveyFinalJourneyLocation keeps the composition-layer OAuth fixture on the
// real httptest Response path; the PG/OAuth journey is added here next.
func surveyFinalJourneyLocation(t *testing.T, recorder *httptest.ResponseRecorder) string {
 t.Helper()
 response := recorder.Result()
 location, err := response.Location()
 if err != nil { t.Fatal(err) }
 return location.String()
}
func TestSurveyFinalJourneyFixtureUsesResponseResult(t *testing.T) {
 recorder:=httptest.NewRecorder(); recorder.Header().Set("Location", "https://example.test/callback?state=opaque")
 if got:=surveyFinalJourneyLocation(t, recorder); got!="https://example.test/callback?state=opaque" { t.Fatalf("location=%q",got) }
}
