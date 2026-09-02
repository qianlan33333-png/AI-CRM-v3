package http

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
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
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	mediaapp "github.com/qianlan33333-png/AI-CRM-v3/internal/media/app"
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
	service, err := mediaapp.NewHTTPFacade(repository)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewHandler(service, handlerTestSecurity{})
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

	for _, endpoint := range []struct {
		path   string
		fields []string
	}{
		{"/api/admin/image-library", []string{"items", "images"}},
		{"/api/admin/attachment-library", []string{"items", "attachments"}},
		{"/api/admin/miniprogram-library", []string{"items", "miniprograms", "mini_programs"}},
		{"/api/admin/group-invite-library", []string{"items", "group_invites"}},
	} {
		empty := responseJSON(t, serve(httptest.NewRequest(http.MethodGet, endpoint.path, nil).WithContext(context.Background())), http.StatusOK)
		for _, field := range endpoint.fields {
			requireJSONArray(t, empty, field)
		}
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
	imageDetail := responseJSON(t, serve(httptest.NewRequest(http.MethodGet, "/api/admin/image-library/"+jsonID(imageID)+"?variant=thumb_160", nil)), http.StatusOK)
	imageItem, ok := imageDetail["image"].(map[string]any)
	if !ok || imageItem["source"] != "upload" || imageItem["thumb_media_id_expires_at"] != "" || imageItem["variant_url"] != "/api/admin/image-library/"+jsonID(imageID)+"/variants/thumb_160" {
		t.Fatalf("image detail compatibility=%#v", imageDetail)
	}
	if got := serve(httptest.NewRequest(http.MethodGet, "/api/admin/image-library/"+jsonID(imageID)+"?variant=unknown", nil)); got.Code != http.StatusBadRequest {
		t.Fatalf("unknown variant status=%d", got.Code)
	}
	missingFileName := admin(httptest.NewRequest(http.MethodPost, "/api/admin/image-library", strings.NewReader(`{"data_url":"data:image/png;base64,`+base64.StdEncoding.EncodeToString(httpPNG(t))+`"}`)))
	if got := serve(missingFileName); got.Code != http.StatusBadRequest {
		t.Fatalf("canonical image must require file_name: status=%d", got.Code)
	}
	overBody := admin(httptest.NewRequest(http.MethodPost, "/api/admin/image-library", strings.NewReader(`{"data_url":"data:image/png;base64,`+base64.StdEncoding.EncodeToString(httpPNG(t))+`","file_name":"cover.png","description":"`+strings.Repeat("x", (10<<20)*4/3+(1<<20)+1)+`"}`)))
	if got := serve(overBody); got.Code != http.StatusBadRequest {
		t.Fatalf("canonical image body cap status=%d", got.Code)
	}
	unknownMini := admin(httptest.NewRequest(http.MethodPost, "/api/admin/miniprogram-library", strings.NewReader(`{"name":"bad","appid":"wx","pagepath":"pages/a","title":"bad","thumb_media_id":"client"}`)))
	if got := serve(unknownMini); got.Code != http.StatusBadRequest {
		t.Fatalf("mini client thumb_media_id status=%d", got.Code)
	}

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
	decimalVersion := admin(httptest.NewRequest(http.MethodPut, "/api/admin/attachment-library/"+jsonID(attachmentID), bytes.NewBufferString(`{"name":"guide3","expected_version":1.5}`)))
	if got := serve(decimalVersion); got.Code != http.StatusBadRequest {
		t.Fatalf("fractional attachment version status=%d", got.Code)
	}
	if _, err = native.Exec(context.Background(), `INSERT INTO media_references(material_kind,material_id,owner,reference_digest) VALUES('attachment',$1,'automation.attachment','sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa')`, attachmentID); err != nil {
		t.Fatal(err)
	}
	attachmentConflict := responseJSON(t, serve(admin(httptest.NewRequest(http.MethodDelete, "/api/admin/attachment-library/"+jsonID(attachmentID), nil))), http.StatusConflict)
	if attachmentConflict["error"] != "attachment_has_references" {
		t.Fatalf("attachment conflict=%#v", attachmentConflict)
	}
	requireJSONFields(t, attachmentConflict, "references")

	miniDetail := responseJSON(t, serve(httptest.NewRequest(http.MethodGet, "/api/admin/miniprogram-library/"+jsonID(int64(compatJSON["item_id"].(float64))), nil)), http.StatusOK)
	requireJSONFields(t, miniDetail, "ok", "item", "miniprogram", "local_only", "provider_call_executed", "real_external_call_executed")
	group := admin(httptest.NewRequest(http.MethodPost, "/api/admin/group-invite-library", bytes.NewBufferString(`{"name":"group","title":"group","join_url":"https://work.weixin.qq.com/gm/a","cover_image_id":`+jsonID(imageID)+`}`)))
	groupResponse := responseJSON(t, serve(group), http.StatusOK)
	requireJSONFields(t, groupResponse, "ok", "item", "group_invite", "item_id", "local_only", "provider_call_executed", "real_external_call_executed")
	groupID := int64(groupResponse["item_id"].(float64))
	decimalCover := admin(httptest.NewRequest(http.MethodPost, "/api/admin/group-invite-library", strings.NewReader(`{"name":"fraction","title":"fraction","join_url":"https://work.weixin.qq.com/gm/a","cover_image_id":`+jsonID(imageID)+`.5}`)))
	if got := serve(decimalCover); got.Code != http.StatusBadRequest {
		t.Fatalf("fractional group cover status=%d", got.Code)
	}
	imageConflict := responseJSON(t, serve(admin(httptest.NewRequest(http.MethodDelete, "/api/admin/image-library/"+jsonID(imageID), nil))), http.StatusConflict)
	if imageConflict["error"] != "image_has_references" {
		t.Fatalf("image conflict=%#v", imageConflict)
	}
	requireJSONFields(t, imageConflict, "references")
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
	sql, err := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "..", "migrations", "0007_media.sql"))
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

func requireJSONArray(t *testing.T, value map[string]any, field string) {
	t.Helper()
	items, exists := value[field]
	if !exists || items == nil {
		t.Fatalf("%s is null or absent in %#v", field, value)
	}
	if _, ok := items.([]any); !ok {
		t.Fatalf("%s is not a JSON array: %#v", field, items)
	}
}
func jsonID(value int64) string { return strconv.FormatInt(value, 10) }
