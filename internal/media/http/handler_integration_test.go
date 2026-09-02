package http

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	mediastore "github.com/qianlan33333-png/AI-CRM-v3/internal/media/store"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

type handlerTestSecurity struct{}

func (handlerTestSecurity) Authenticate(_ context.Context, request *http.Request) (accessdomain.Principal, error) {
	if request.Header.Get("X-Test-Auth") == "none" {
		return accessdomain.Principal{}, errors.New("unauthorized")
	}
	if request.Header.Get("X-Test-Role") == "viewer" {
		return accessdomain.Principal{Kind: accessdomain.KindAdmin, InternalID: 8, Roles: []accessdomain.Role{accessdomain.RoleViewer}}, nil
	}
	return accessdomain.Principal{Kind: accessdomain.KindAdmin, InternalID: 7, Roles: []accessdomain.Role{accessdomain.RoleAdmin}}, nil
}
func (handlerTestSecurity) AuthorizeCSRF(ctx context.Context, request *http.Request) (accessdomain.Principal, error) {
	if request.Header.Get("X-CSRF-Token") != "test-csrf" {
		return accessdomain.Principal{}, accessdomain.ErrCSRFRequired
	}
	return handlerTestSecurity{}.Authenticate(ctx, request)
}

func TestMediaHTTPCompatibilitySecurityAndFrozenWriteContract(t *testing.T) {
	url, urlErr := platformconfig.DatabaseURL()
	if urlErr != nil {
		t.Skip("database URL not configured")
	}
	repository, closeRepository, native := newHTTPIntegrationRepository(t, url)
	defer closeRepository()
	handler, err := NewHandler(repository, handlerTestSecurity{})
	if err != nil {
		t.Fatal(err)
	}
	serve := func(request *http.Request) *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		return recorder
	}
	admin := func(request *http.Request) *http.Request {
		request.Header.Set("X-CSRF-Token", "test-csrf")
		return request
	}

	if got := serve(httptest.NewRequest(http.MethodGet, "/api/admin/image-library", nil).WithContext(context.Background())); got.Code != http.StatusOK {
		t.Fatalf("admin read status=%d", got.Code)
	}
	unauthenticated := httptest.NewRequest(http.MethodGet, "/api/admin/image-library", nil)
	unauthenticated.Header.Set("X-Test-Auth", "none")
	if got := serve(unauthenticated); got.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status=%d", got.Code)
	}
	viewer := httptest.NewRequest(http.MethodPost, "/api/admin/miniprogram-library", bytes.NewBufferString(`{"name":"viewer","appid":"wx1","pagepath":"pages/a","title":"viewer"}`))
	viewer.Header.Set("X-Test-Role", "viewer")
	viewer.Header.Set("X-CSRF-Token", "test-csrf")
	if got := serve(viewer); got.Code != http.StatusForbidden {
		t.Fatalf("viewer write status=%d", got.Code)
	}
	if got := serve(httptest.NewRequest(http.MethodPost, "/api/admin/miniprogram-library", bytes.NewBufferString(`{}`))); got.Code != http.StatusForbidden {
		t.Fatalf("missing csrf status=%d", got.Code)
	}
	badCSRF := httptest.NewRequest(http.MethodPost, "/api/admin/miniprogram-library", bytes.NewBufferString(`{}`))
	badCSRF.Header.Set("X-CSRF-Token", "bad")
	if got := serve(badCSRF); got.Code != http.StatusForbidden {
		t.Fatalf("bad csrf status=%d", got.Code)
	}

	miniBody := `{"name":"compat","appid":"wx123","pagepath":"pages/a","title":"card"}`
	compat := serve(admin(httptest.NewRequest(http.MethodPost, "/api/admin/miniprogram-library", bytes.NewBufferString(miniBody))))
	compatJSON := responseJSON(t, compat, http.StatusOK)
	requireJSONFields(t, compatJSON, "ok", "item", "miniprogram", "item_id", "changed", "thumb_resolve", "local_only", "provider_call_executed", "real_external_call_executed")
	var serverCompatAudit int
	if err = native.QueryRow(context.Background(), `SELECT count(*) FROM media_audit_events WHERE payload->>'idempotency_source'='server_compat'`).Scan(&serverCompatAudit); err != nil || serverCompatAudit != 1 {
		t.Fatalf("server compatibility audit=%d err=%v", serverCompatAudit, err)
	}
	key := "explicit-mini-key-0001"
	firstRequest := admin(httptest.NewRequest(http.MethodPost, "/api/admin/miniprogram-library", bytes.NewBufferString(`{"name":"replay","appid":"wx123","pagepath":"pages/b","title":"card"}`)))
	firstRequest.Header.Set("Idempotency-Key", key)
	first := responseJSON(t, serve(firstRequest), http.StatusOK)
	replayRequest := admin(httptest.NewRequest(http.MethodPost, "/api/admin/miniprogram-library", bytes.NewBufferString(`{"name":"replay","appid":"wx123","pagepath":"pages/b","title":"card"}`)))
	replayRequest.Header.Set("Idempotency-Key", key)
	replay := responseJSON(t, serve(replayRequest), http.StatusOK)
	if first["item_id"] != replay["item_id"] {
		t.Fatalf("replay changed item: first=%v replay=%v", first, replay)
	}
	drift := admin(httptest.NewRequest(http.MethodPost, "/api/admin/miniprogram-library", bytes.NewBufferString(`{"name":"drift","appid":"wx123","pagepath":"pages/b","title":"card"}`)))
	drift.Header.Set("Idempotency-Key", key)
	if got := serve(drift); got.Code != http.StatusConflict {
		t.Fatalf("payload drift status=%d", got.Code)
	}

	imageResponse := responseJSON(t, serve(multipartImageRequest(t, "/api/admin/image-library/upload", "image", "cover.png", httpPNG(t))), http.StatusForbidden)
	_ = imageResponse
	imageRequest := multipartImageRequest(t, "/api/admin/image-library/upload", "image", "cover.png", httpPNG(t))
	imageRequest.Header.Set("X-CSRF-Token", "test-csrf")
	image := responseJSON(t, serve(imageRequest), http.StatusOK)
	requireJSONFields(t, image, "ok", "item", "image", "item_id", "source_status", "storage_adapter_mode")
	imageID := int64(image["item_id"].(float64))

	attachmentRequest := multipartImageRequest(t, "/api/admin/attachment-library/upload", "attachment", "guide.pdf", []byte("%PDF-1.4\n1 0 obj\n<<>>\nendobj\ntrailer\n<<>>\n%%EOF\n"))
	attachmentRequest.Header.Set("X-CSRF-Token", "test-csrf")
	attachment := responseJSON(t, serve(attachmentRequest), http.StatusOK)
	requireJSONFields(t, attachment, "ok", "item", "attachment", "id", "version", "download_url")
	attachmentID := int64(attachment["id"].(float64))
	cas := admin(httptest.NewRequest(http.MethodPut, "/api/admin/attachment-library/"+jsonID(attachmentID), bytes.NewBufferString(`{"name":"guide2","expected_version":1}`)))
	cas.Header.Set("Idempotency-Key", "attachment-cas-key-0001")
	if got := serve(cas); got.Code != http.StatusOK {
		t.Fatalf("attachment cas status=%d", got.Code)
	}
	stale := admin(httptest.NewRequest(http.MethodPut, "/api/admin/attachment-library/"+jsonID(attachmentID), bytes.NewBufferString(`{"name":"guide3","expected_version":1}`)))
	stale.Header.Set("Idempotency-Key", "attachment-cas-key-0002")
	if got := serve(stale); got.Code != http.StatusConflict {
		t.Fatalf("attachment stale cas status=%d", got.Code)
	}

	miniDetail := responseJSON(t, serve(httptest.NewRequest(http.MethodGet, "/api/admin/miniprogram-library/"+jsonID(int64(compatJSON["item_id"].(float64))), nil)), http.StatusOK)
	requireJSONFields(t, miniDetail, "ok", "item", "miniprogram", "local_only", "provider_call_executed", "real_external_call_executed")
	group := admin(httptest.NewRequest(http.MethodPost, "/api/admin/group-invite-library", bytes.NewBufferString(`{"name":"group","title":"group","join_url":"https://work.weixin.qq.com/gm/a","cover_image_id":`+jsonID(imageID)+`}`)))
	groupResponse := responseJSON(t, serve(group), http.StatusOK)
	requireJSONFields(t, groupResponse, "ok", "item", "group_invite", "item_id", "local_only", "provider_call_executed", "real_external_call_executed")
	groupID := int64(groupResponse["item_id"].(float64))
	groupDetail := responseJSON(t, serve(httptest.NewRequest(http.MethodGet, "/api/admin/group-invite-library/"+jsonID(groupID), nil)), http.StatusOK)
	requireJSONFields(t, groupDetail, "ok", "item", "group_invite", "provider_call_executed")
	archive := admin(httptest.NewRequest(http.MethodDelete, "/api/admin/group-invite-library/"+jsonID(groupID), nil))
	archive.Header.Set("Idempotency-Key", "group-archive-key-0001")
	if got := serve(archive); got.Code != http.StatusOK {
		t.Fatalf("group archive status=%d", got.Code)
	}
}

func newHTTPIntegrationRepository(t *testing.T, url string) (*mediastore.Repository, func(), *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	raw := make([]byte, 6)
	if _, err = rand.Read(raw); err != nil {
		t.Fatal(err)
	}
	schema := "media_http_" + hex.EncodeToString(raw)
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	native, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	_, file, _, _ := runtime.Caller(0)
	sql, err := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "..", "migrations", "0006_media.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = native.Exec(ctx, string(sql)); err != nil {
		t.Fatal(err)
	}
	pool, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	uow, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		t.Fatal(err)
	}
	repository, err := mediastore.NewPostgreSQL(native, uow)
	if err != nil {
		t.Fatal(err)
	}
	return repository, func() {
		pool.Close()
		native.Close()
		_, _ = admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE")
		admin.Close()
	}, native
}

func multipartImageRequest(t *testing.T, path, field, fileName string, content []byte) *http.Request {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	header := textproto.MIMEHeader{}
	header.Set("Content-Disposition", mime.FormatMediaType("form-data", map[string]string{"name": field, "filename": fileName}))
	if field == "image" {
		header.Set("Content-Type", "image/png")
	} else {
		header.Set("Content-Type", "application/pdf")
	}
	part, err := writer.CreatePart(header)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = part.Write(content); err != nil {
		t.Fatal(err)
	}
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, path, &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	return request
}
func httpPNG(t *testing.T) []byte {
	t.Helper()
	value := image.NewRGBA(image.Rect(0, 0, 2, 2))
	value.Set(0, 0, color.RGBA{R: 255, A: 255})
	var out bytes.Buffer
	if err := png.Encode(&out, value); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}
func responseJSON(t *testing.T, recorder *httptest.ResponseRecorder, wanted int) map[string]any {
	t.Helper()
	if recorder.Code != wanted {
		t.Fatalf("status=%d body=%s, wanted=%d", recorder.Code, recorder.Body.String(), wanted)
	}
	var value map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &value); err != nil {
		t.Fatalf("json body=%q err=%v", recorder.Body.String(), err)
	}
	return value
}
func requireJSONFields(t *testing.T, value map[string]any, fields ...string) {
	t.Helper()
	for _, field := range fields {
		if _, ok := value[field]; !ok {
			t.Fatalf("missing %q in %#v", field, value)
		}
	}
}
func jsonID(value int64) string { return strconv.FormatInt(value, 10) }
