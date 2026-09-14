package http

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	overviewapp "github.com/qianlan33333-png/AI-CRM-v3/internal/overview/app"
)

type overviewSecurityStub struct {
	principal accessdomain.Principal
	err       error
}

func (stub overviewSecurityStub) Authenticate(context.Context, *http.Request) (accessdomain.Principal, error) {
	return stub.principal, stub.err
}

type overviewReaderStub struct {
	calls int
	query overviewapp.Query
	err   error
}

func (stub *overviewReaderStub) Read(_ context.Context, query overviewapp.Query) (overviewapp.Response, error) {
	stub.calls++
	stub.query = query
	if stub.err != nil {
		return overviewapp.Response{}, stub.err
	}
	return overviewapp.Response{Range: query.Range}, nil
}

func TestHandlerStopsBeforeAnyAggregateForUnauthenticatedOrUnauthorizedCaller(t *testing.T) {
	for _, row := range []struct {
		name     string
		security overviewSecurityStub
		wantCode int
	}{
		{name: "unauthenticated", security: overviewSecurityStub{err: errors.New("no session")}, wantCode: http.StatusUnauthorized},
		{name: "customer", security: overviewSecurityStub{principal: accessdomain.Principal{Kind: accessdomain.KindCustomer, InternalID: 9, Roles: []accessdomain.Role{accessdomain.RoleViewer}}}, wantCode: http.StatusForbidden},
		{name: "staff without role", security: overviewSecurityStub{principal: accessdomain.Principal{Kind: accessdomain.KindStaff, InternalID: 9}}, wantCode: http.StatusForbidden},
	} {
		t.Run(row.name, func(t *testing.T) {
			reader := &overviewReaderStub{}
			handler, err := NewHandler(Config{Reader: reader, Security: row.security, Now: func() time.Time { return time.Date(2026, 9, 15, 1, 0, 0, 0, time.UTC) }})
			if err != nil {
				t.Fatal(err)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, overviewPath+"?period=today", nil))
			if response.Code != row.wantCode || reader.calls != 0 {
				t.Fatalf("status=%d calls=%d", response.Code, reader.calls)
			}
		})
	}
}

func TestHandlerUsesBeijingHalfOpenWindowForAdminViewer(t *testing.T) {
	reader := &overviewReaderStub{}
	now := time.Date(2026, 9, 14, 16, 30, 0, 0, time.UTC) // 00:30 in Beijing on the 15th.
	handler, err := NewHandler(Config{
		Reader:   reader,
		Security: overviewSecurityStub{principal: accessdomain.Principal{Kind: accessdomain.KindAdmin, InternalID: 1, Roles: []accessdomain.Role{accessdomain.RoleViewer}}},
		Now:      func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, overviewPath+"?period=today", nil))
	if response.Code != http.StatusOK || reader.calls != 1 {
		t.Fatalf("status=%d calls=%d body=%s", response.Code, reader.calls, response.Body.String())
	}
	wantStart := time.Date(2026, 9, 14, 16, 0, 0, 0, time.UTC)
	wantEnd := time.Date(2026, 9, 15, 16, 0, 0, 0, time.UTC)
	if reader.query.Range.Period != "today" || reader.query.Range.Timezone != "Asia/Shanghai" || !reader.query.Range.Start.Equal(wantStart) || !reader.query.Range.End.Equal(wantEnd) {
		t.Fatalf("window=%+v", reader.query.Range)
	}
}

func TestHandlerRejectsInvalidCustomWindowAndDuplicateQueryBeforeRead(t *testing.T) {
	reader := &overviewReaderStub{}
	handler, err := NewHandler(Config{
		Reader:   reader,
		Security: overviewSecurityStub{principal: accessdomain.Principal{Kind: accessdomain.KindAdmin, InternalID: 1, Roles: []accessdomain.Role{accessdomain.RoleAdmin}}},
		Now:      time.Now,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{
		overviewPath + "?period=custom&from=2026-09-15",
		overviewPath + "?period=custom&from=2026-09-16&to=2026-09-15",
		overviewPath + "?period=today&period=7d",
		overviewPath + "?period=7d&from=2026-09-15",
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, target, nil))
		if response.Code != http.StatusBadRequest {
			t.Fatalf("target=%s status=%d", target, response.Code)
		}
	}
	if reader.calls != 0 {
		t.Fatalf("invalid query reached reader %d times", reader.calls)
	}
}
