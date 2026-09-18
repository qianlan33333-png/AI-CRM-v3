package http

import (
	"context"
	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	segmentport "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/port"
	"net/http/httptest"
	"testing"
)

type supervisionStub struct {
	calls  int
	source string
}

func (s *supervisionStub) RecordSupervisedPush(_ context.Context, source, key string, p segmentport.CorePush) (segmentport.CorePush, error) {
	s.calls++
	s.source = source
	return p, nil
}
func TestCoreSupervisionRequiresMachineWriteAndIdempotency(t *testing.T) {
	for _, tc := range []struct {
		name                 string
		scopes, capabilities []string
		key                  string
		want                 int
	}{
		{"read only", []string{"read"}, []string{"external_read"}, "push-report-00000001", 403},
		{"missing capability", []string{"write"}, nil, "push-report-00000001", 403},
		{"missing key", []string{"write"}, []string{"external_write"}, "", 400},
		{"authorized", []string{"write"}, []string{"external_write"}, "push-report-00000001", 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newV1Handler(t, accessdomain.MachinePrincipal{ClientID: "supervisor-a", Scopes: tc.scopes, Capabilities: tc.capabilities}, &handlerOperationStub{})
			stub := &supervisionStub{}
			h.coreSupervision = stub
			req := machineRequest("POST", "https://crm.example.com/open/v1/audience/push-records", `{"customer_id":7,"package_id":2,"source":"spoofed"}`)
			req.Header.Set("Idempotency-Key", tc.key)
			w := httptest.NewRecorder()
			h.Routes().ServeHTTP(w, req)
			if w.Code != tc.want {
				t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
			}
			if tc.want == 200 {
				if stub.calls != 1 || stub.source != "supervisor-a" {
					t.Fatal("caller identity not bound")
				}
			} else if stub.calls != 0 {
				t.Fatal("unauthorized invocation")
			}
		})
	}
}
