package http

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	configapp "github.com/qianlan33333-png/AI-CRM-v3/internal/config/app"
	configport "github.com/qianlan33333-png/AI-CRM-v3/internal/config/port"
	configstore "github.com/qianlan33333-png/AI-CRM-v3/internal/config/store"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	"net/http/httptest"
)

// OneID decision: not involved. This JSDOM regression journey mutates only Config-owned numeric
// runtime policy. Persistence decision: the actual HTTP handler reaches the
// real Config store, whose release, pointer, audit, receipt, and outbox share
// one PostgreSQL UoW; no Provider is constructed.
func TestRuntimeReleaseHostJSDOMJourneyUsesActualPostgreSQLHTTP(t *testing.T) {
	pool, cleanup := runtimeReleaseBrowserPool(t)
	defer cleanup()
	ctx := context.Background()
	wrapped, err := platformpostgres.Wrap(pool, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer wrapped.Close()
	uow, err := platformpostgres.NewUnitOfWork(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	repository, err := configstore.NewPostgreSQL(pool, uow)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := configapp.NewRuntimeReleaseService(uow, repository, repository, 1)
	if err != nil {
		t.Fatal(err)
	}
	principal := accessdomain.Principal{InternalID: 7, Kind: accessdomain.KindAdmin, Roles: []accessdomain.Role{accessdomain.RoleAdmin}}
	handler, err := NewHandler(&testSettings{}, &testWizard{}, newTestConfig(), testProjections{}, testSecurity{principal: principal}, runtime)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	_, file, _, ok := goruntime.Caller(0)
	if !ok {
		t.Fatal("locate runtime release browser journey")
	}
	script := filepath.Join(filepath.Dir(file), "..", "..", "webshell", "runtime_config_releases_pg.test.mjs")
	command := exec.CommandContext(ctx, "node", script)
	command.Env = append(os.Environ(), "AICRM_RUNTIME_RELEASE_TEST_URL="+server.URL)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("runtime release browser journey: %v output=%s", err, output)
	}
	var releases, published, superseded, usage int
	if err = pool.QueryRow(ctx, `SELECT count(*),count(*) FILTER (WHERE state='published'),count(*) FILTER (WHERE state='superseded') FROM config_runtime_releases`).Scan(&releases, &published, &superseded); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM config_runtime_usage`).Scan(&usage); err != nil {
		t.Fatal(err)
	}
	if releases != 2 || published != 1 || superseded != 1 || usage != 0 {
		t.Fatalf("browser release facts releases/published/superseded/usage=%d/%d/%d/%d output=%s", releases, published, superseded, usage, output)
	}
}

func runtimeReleaseBrowserPool(t *testing.T) (*pgxpool.Pool, func()) {
	t.Helper()
	url, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Skip("AICRM_DATABASE_URL is not configured; skipping runtime release browser PostgreSQL journey")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var random [8]byte
	if _, err = rand.Read(random[:]); err != nil {
		t.Fatal(err)
	}
	schema := "runtime_release_browser_" + hex.EncodeToString(random[:])
	admin, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
		admin.Close(ctx)
		t.Fatal(err)
	}
	config, err := pgxpool.ParseConfig(url)
	if err != nil {
		admin.Close(ctx)
		t.Fatal(err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		admin.Close(ctx)
		t.Fatal(err)
	}
	_, file, _, ok := goruntime.Caller(0)
	if !ok {
		pool.Close()
		admin.Close(ctx)
		t.Fatal("locate runtime release browser migration")
	}
	for _, name := range []string{"0013_automation_agents.sql", "0015_config_adminops.sql", "0043_automation_runtime.sql", "0094_runtime_config_releases.sql", "0102_config_center_runtime_application.sql"} {
		payload, readErr := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "..", "migrations", name))
		if readErr != nil {
			pool.Close()
			admin.Close(ctx)
			t.Fatal(readErr)
		}
		if _, execErr := pool.Exec(ctx, string(payload)); execErr != nil {
			pool.Close()
			admin.Close(ctx)
			t.Fatalf("apply %s: %v", name, execErr)
		}
	}
	return pool, func() {
		pool.Close()
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		_, _ = admin.Exec(cleanupCtx, "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
		admin.Close(cleanupCtx)
	}
}

// OneID decision: CorpID and Open Platform scope are identity boundaries, so
// this PostgreSQL regression proves that Config may retain an existing value
// but cannot publish an ordinary scope switch. No Provider is constructed.
func TestRuntimeReleaseRejectsBoundIdentityScopeChangePostgreSQL(t *testing.T) {
	pool, cleanup := runtimeReleaseBrowserPool(t)
	defer cleanup()
	ctx := context.Background()
	wrapped, err := platformpostgres.Wrap(pool, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer wrapped.Close()
	uow, err := platformpostgres.NewUnitOfWork(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	repository, err := configstore.NewPostgreSQL(pool, uow)
	if err != nil {
		t.Fatal(err)
	}
	setting := func(key configport.RuntimeSettingKey, value any) configport.RuntimeSetting {
		raw, marshalErr := json.Marshal(value)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		return configport.RuntimeSetting{Key: key, Value: raw}
	}
	service, err := configapp.NewRuntimeReleaseService(uow, repository, repository, 1, configapp.WithRuntimeDefaults([]configport.RuntimeSetting{
		setting(configport.AutomationOperationsMaxRecipientsPerRun, 1),
		setting(configport.RuntimeWeComCorpID, "wx-bound-corp"),
		setting(configport.WeChatPayAppID, "wx-pay-bound"),
		setting(configport.WeChatPayAppScope, "wechat-app:bound"),
		setting(configport.WeChatPayH5AppID, "wx-pay-h5-bound"),
		setting(configport.WeChatPayH5AppScope, "wechat-h5:bound"),
		setting(configport.WeChatPayMerchantID, "mch-bound"),
		setting(configport.WeChatShopAppID, "shop-bound"),
		setting(configport.SurveyOAuthAppID, "wx-survey-bound"),
		setting(configport.SurveyOAuthOpenPlatformID, "wx-open-platform-bound"),
	}))
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range []configport.RuntimeSetting{
		setting(configport.RuntimeWeComCorpID, "wx-other-corp"),
		setting(configport.WeChatPayAppID, "wx-pay-other"),
		setting(configport.WeChatPayAppScope, "wechat-app:other"),
		setting(configport.WeChatPayH5AppID, "wx-pay-h5-other"),
		setting(configport.WeChatPayH5AppScope, "wechat-h5:other"),
		setting(configport.WeChatPayMerchantID, "mch-other"),
		setting(configport.WeChatShopAppID, "shop-other"),
		setting(configport.SurveyOAuthAppID, "wx-survey-other"),
		setting(configport.SurveyOAuthOpenPlatformID, "wx-open-platform-other"),
	} {
		draft, createErr := service.CreateRuntimeReleaseDraft(ctx, configport.RuntimeReleaseDraftCommand{ExpectedBaseRevision: 0, Settings: []configport.RuntimeSetting{candidate}, Actor: "admin:7", IdempotencyKey: "scope-bound-create-" + string(candidate.Key)})
		if createErr != nil {
			t.Fatalf("create %s: %v", candidate.Key, createErr)
		}
		validated, validateErr := service.ValidateRuntimeRelease(ctx, configport.RuntimeReleaseMutationCommand{ReleaseID: draft.ID, Actor: "admin:7", IdempotencyKey: "scope-bound-validate-" + string(candidate.Key)})
		if validateErr != nil || validated.State != configport.RuntimeReleaseValidationFailed || len(validated.ValidationErrors) == 0 {
			t.Fatalf("scope %s validation=%#v err=%v", candidate.Key, validated, validateErr)
		}
	}
	initial, err := configapp.NewRuntimeReleaseService(uow, repository, repository, 1, configapp.WithRuntimeDefaults([]configport.RuntimeSetting{
		setting(configport.AutomationOperationsMaxRecipientsPerRun, 1),
		setting(configport.WeChatPayMerchantID, ""),
		setting(configport.WeChatPayMerchantSerial, "serial-initial"),
		setting(configport.WeChatShopAppID, ""),
	}))
	if err != nil {
		t.Fatal(err)
	}
	first, err := initial.CreateRuntimeReleaseDraft(ctx, configport.RuntimeReleaseDraftCommand{ExpectedBaseRevision: 0, Settings: []configport.RuntimeSetting{
		setting(configport.WeChatPayMerchantID, "mch-first"),
		setting(configport.WeChatShopAppID, "shop-first"),
	}, Actor: "admin:7", IdempotencyKey: "scope-bound-initial-integration"})
	if err != nil {
		t.Fatal(err)
	}
	first, err = initial.ValidateRuntimeRelease(ctx, configport.RuntimeReleaseMutationCommand{ReleaseID: first.ID, Actor: "admin:7", IdempotencyKey: "scope-bound-initial-integration-validate"})
	if err != nil || first.State != configport.RuntimeReleaseValidated {
		t.Fatalf("initial integration validation=%#v err=%v", first, err)
	}
	first, err = initial.PublishRuntimeRelease(ctx, configport.RuntimeReleasePublishCommand{ReleaseID: first.ID, ExpectedBaseRevision: 0, ExpectedChecksum: first.Checksum, Actor: "admin:7", IdempotencyKey: "scope-bound-initial-integration-publish"})
	if err != nil || first.State != configport.RuntimeReleasePublished {
		t.Fatalf("initial integration publication=%#v err=%v", first, err)
	}
	serial, err := initial.CreateRuntimeReleaseDraft(ctx, configport.RuntimeReleaseDraftCommand{ExpectedBaseRevision: first.ID, Settings: []configport.RuntimeSetting{
		setting(configport.WeChatPayMerchantSerial, "serial-rotated"),
	}, Actor: "admin:7", IdempotencyKey: "scope-bound-merchant-serial"})
	if err != nil {
		t.Fatal(err)
	}
	serial, err = initial.ValidateRuntimeRelease(ctx, configport.RuntimeReleaseMutationCommand{ReleaseID: serial.ID, Actor: "admin:7", IdempotencyKey: "scope-bound-merchant-serial-validate"})
	if err != nil || serial.State != configport.RuntimeReleaseValidated {
		t.Fatalf("merchant serial rotation validation=%#v err=%v", serial, err)
	}
}

func TestRuntimeApplicationFactRequiresExactSnapshotChecksumPostgreSQL(t *testing.T) {
	pool, cleanup := runtimeReleaseBrowserPool(t)
	defer cleanup()
	ctx := context.Background()
	wrapped, err := platformpostgres.Wrap(pool, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer wrapped.Close()
	uow, err := platformpostgres.NewUnitOfWork(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	repository, err := configstore.NewPostgreSQL(pool, uow)
	if err != nil {
		t.Fatal(err)
	}
	service, err := configapp.NewRuntimeReleaseService(uow, repository, repository, 1)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := service.EffectiveSnapshot(ctx)
	if err != nil || len(snapshot.Checksum) != 64 {
		t.Fatalf("snapshot=%#v err=%v", snapshot, err)
	}
	if err = service.RecordRuntimeApplication(ctx, configport.RuntimeApplication{Revision: snapshot.Revision, Source: snapshot.Source, Role: "api", ReleaseSHA: "config-test", SnapshotChecksum: snapshot.Checksum, AppliedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	applications, err := service.ListRuntimeApplications(ctx, 10)
	if err != nil || len(applications) != 1 || applications[0].SnapshotChecksum != snapshot.Checksum {
		t.Fatalf("applications=%#v err=%v", applications, err)
	}
	if err = service.RecordRuntimeApplication(ctx, configport.RuntimeApplication{Revision: snapshot.Revision, Source: snapshot.Source, Role: "api", ReleaseSHA: "config-test", SnapshotChecksum: "bad", AppliedAt: time.Now().UTC()}); err == nil {
		t.Fatal("expected invalid checksum rejection")
	}
}

// OneID decision: not involved. This regression saves Config-owned runtime
// data through the actual HTTP handler and PostgreSQL UoW; no Provider is
// constructed. It proves JSON string values survive a Config Center open and
// untouched save, rather than being JSON-parsed a second time by the browser.
func TestConfigCenterHostKeepsNativeStringSettingsOnActualPostgreSQLHTTP(t *testing.T) {
	pool, cleanup := runtimeReleaseBrowserPool(t)
	defer cleanup()
	ctx := context.Background()
	wrapped, err := platformpostgres.Wrap(pool, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer wrapped.Close()
	uow, err := platformpostgres.NewUnitOfWork(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	repository, err := configstore.NewPostgreSQL(pool, uow)
	if err != nil {
		t.Fatal(err)
	}
	setting := func(key configport.RuntimeSettingKey, value any) configport.RuntimeSetting {
		raw, marshalErr := json.Marshal(value)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		return configport.RuntimeSetting{Key: key, Value: raw}
	}
	runtime, err := configapp.NewRuntimeReleaseService(uow, repository, repository, 1, configapp.WithRuntimeDefaults([]configport.RuntimeSetting{
		setting(configport.AutomationOperationsMaxRecipientsPerRun, 1),
		setting(configport.AutomationOperationsProviderMode, "limited"),
		setting(configport.WeComEnabled, false),
		setting(configport.RuntimeWeComCorpID, ""),
		setting(configport.RuntimeWeComAgentID, "agent-preserved"),
		setting(configport.WeComCallbackEnabled, false),
		setting(configport.WeComCustomerSyncEnabled, false),
		setting(configport.MessageArchiveEnabled, false),
		setting(configport.MessageArchivePageLimit, 1),
		setting(configport.MessageArchivePageBudget, 1),
		setting(configport.SidebarContextTokenTTLSeconds, 60),
	}))
	if err != nil {
		t.Fatal(err)
	}
	principal := accessdomain.Principal{InternalID: 7, Kind: accessdomain.KindAdmin, Roles: []accessdomain.Role{accessdomain.RoleAdmin}}
	handler, err := NewHandler(&testSettings{}, &testWizard{}, newTestConfig(), testProjections{}, testSecurity{principal: principal}, runtime)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	_, file, _, ok := goruntime.Caller(0)
	if !ok {
		t.Fatal("locate Config Center browser journey")
	}
	command := exec.CommandContext(ctx, "node", filepath.Join(filepath.Dir(file), "..", "..", "webshell", "config_center_host_pg.test.mjs"))
	command.Env = append(os.Environ(), "AICRM_RUNTIME_RELEASE_TEST_URL="+server.URL)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("Config Center browser journey: %v output=%s", err, output)
	}
	if !strings.Contains(string(output), "config_center_host_pg: PASS") {
		t.Fatalf("Config Center browser journey did not report success: %q", output)
	}
}

// OneID decision: this checks that an identity binding created by a Config
// release becomes just as immutable as a deployment binding on the next
// release. Persistence is Config-owned PostgreSQL only; no Provider runs.
func TestRuntimeReleaseRejectsRebindingScopeFromPublishedSnapshotPostgreSQL(t *testing.T) {
	pool, cleanup := runtimeReleaseBrowserPool(t)
	defer cleanup()
	ctx := context.Background()
	wrapped, err := platformpostgres.Wrap(pool, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer wrapped.Close()
	uow, err := platformpostgres.NewUnitOfWork(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	repository, err := configstore.NewPostgreSQL(pool, uow)
	if err != nil {
		t.Fatal(err)
	}
	setting := func(key configport.RuntimeSettingKey, value any) configport.RuntimeSetting {
		raw, marshalErr := json.Marshal(value)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		return configport.RuntimeSetting{Key: key, Value: raw}
	}
	service, err := configapp.NewRuntimeReleaseService(uow, repository, repository, 1, configapp.WithRuntimeDefaults([]configport.RuntimeSetting{
		setting(configport.AutomationOperationsMaxRecipientsPerRun, 1),
		setting(configport.WeChatPayAppID, ""),
	}))
	if err != nil {
		t.Fatal(err)
	}
	first, err := service.CreateRuntimeReleaseDraft(ctx, configport.RuntimeReleaseDraftCommand{
		ExpectedBaseRevision: 0, Settings: []configport.RuntimeSetting{setting(configport.WeChatPayAppID, "wx-first-binding")}, Actor: "admin:7", IdempotencyKey: "first-scope-binding",
	})
	if err != nil {
		t.Fatal(err)
	}
	first, err = service.ValidateRuntimeRelease(ctx, configport.RuntimeReleaseMutationCommand{ReleaseID: first.ID, Actor: "admin:7", IdempotencyKey: "validate-first-scope-binding"})
	if err != nil || first.State != configport.RuntimeReleaseValidated {
		t.Fatalf("first binding validation release=%#v err=%v", first, err)
	}
	first, err = service.PublishRuntimeRelease(ctx, configport.RuntimeReleasePublishCommand{ReleaseID: first.ID, ExpectedBaseRevision: 0, ExpectedChecksum: first.Checksum, Actor: "admin:7", IdempotencyKey: "publish-first-scope-binding"})
	if err != nil || first.State != configport.RuntimeReleasePublished {
		t.Fatalf("first binding publish release=%#v err=%v", first, err)
	}
	second, err := service.CreateRuntimeReleaseDraft(ctx, configport.RuntimeReleaseDraftCommand{
		ExpectedBaseRevision: first.ID, Settings: []configport.RuntimeSetting{setting(configport.WeChatPayAppID, "wx-second-binding")}, Actor: "admin:7", IdempotencyKey: "second-scope-binding",
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err = service.ValidateRuntimeRelease(ctx, configport.RuntimeReleaseMutationCommand{ReleaseID: second.ID, Actor: "admin:7", IdempotencyKey: "validate-second-scope-binding"})
	if err != nil || second.State != configport.RuntimeReleaseValidationFailed || len(second.ValidationErrors) == 0 {
		t.Fatalf("published scope rebinding release=%#v err=%v", second, err)
	}
}

// OneID decision: this is a Config release compatibility test, not an
// identity resolution path. It uses the actual Config PostgreSQL UoW and no
// Provider calls. The recovery action is safe only when new-catalog settings
// already equal protected deployment defaults.
func TestPrepareLegacyRuntimeRecoveryPostgreSQL(t *testing.T) {
	pool, cleanup := runtimeReleaseBrowserPool(t)
	defer cleanup()
	ctx := context.Background()
	wrapped, err := platformpostgres.Wrap(pool, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer wrapped.Close()
	uow, err := platformpostgres.NewUnitOfWork(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	repository, err := configstore.NewPostgreSQL(pool, uow)
	if err != nil {
		t.Fatal(err)
	}
	setting := func(key configport.RuntimeSettingKey, value any) configport.RuntimeSetting {
		raw, marshalErr := json.Marshal(value)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		return configport.RuntimeSetting{Key: key, Value: raw}
	}
	service, err := configapp.NewRuntimeReleaseService(uow, repository, repository, 1, configapp.WithRuntimeDefaults([]configport.RuntimeSetting{
		setting(configport.AutomationOperationsMaxRecipientsPerRun, 1),
		setting(configport.WorkerLimit, 1),
	}))
	if err != nil {
		t.Fatal(err)
	}
	publish := func(expected int64, key configport.RuntimeSettingKey, value any, suffix string) configport.RuntimeRelease {
		draft, createErr := service.CreateRuntimeReleaseDraft(ctx, configport.RuntimeReleaseDraftCommand{ExpectedBaseRevision: expected, Settings: []configport.RuntimeSetting{setting(key, value)}, Actor: "admin:7", IdempotencyKey: "legacy-recovery-create-" + suffix})
		if createErr != nil {
			t.Fatal(createErr)
		}
		draft, validateErr := service.ValidateRuntimeRelease(ctx, configport.RuntimeReleaseMutationCommand{ReleaseID: draft.ID, Actor: "admin:7", IdempotencyKey: "legacy-recovery-validate-" + suffix})
		if validateErr != nil || draft.State != configport.RuntimeReleaseValidated {
			t.Fatalf("validate %s release=%#v err=%v", suffix, draft, validateErr)
		}
		published, publishErr := service.PublishRuntimeRelease(ctx, configport.RuntimeReleasePublishCommand{ReleaseID: draft.ID, ExpectedBaseRevision: expected, ExpectedChecksum: draft.Checksum, Actor: "admin:7", IdempotencyKey: "legacy-recovery-publish-" + suffix})
		if publishErr != nil || published.State != configport.RuntimeReleasePublished {
			t.Fatalf("publish %s release=%#v err=%v", suffix, published, publishErr)
		}
		return published
	}
	active := publish(0, configport.AutomationOperationsMaxRecipientsPerRun, 5000, "max")
	recovery, err := service.PrepareLegacyRuntimeRecovery(ctx, configport.RuntimeReleaseLegacyRecoveryCommand{ExpectedBaseRevision: active.ID, Actor: "admin:7", IdempotencyKey: "legacy-recovery-compatible"})
	if err != nil || recovery.State != configport.RuntimeReleasePublished || len(recovery.Settings) != 1 || recovery.Settings[0].Key != configport.AutomationOperationsMaxRecipientsPerRun || string(recovery.Settings[0].Value) != "5000" {
		t.Fatalf("compatibility recovery=%#v err=%v", recovery, err)
	}
	active = publish(recovery.ID, configport.WorkerLimit, 2, "worker-limit")
	if _, err = service.PrepareLegacyRuntimeRecovery(ctx, configport.RuntimeReleaseLegacyRecoveryCommand{ExpectedBaseRevision: active.ID, Actor: "admin:7", IdempotencyKey: "legacy-recovery-divergent"}); !errors.Is(err, configport.ErrRuntimeReleaseConflict) {
		t.Fatalf("expected newer-catalog divergence rejection, got %v", err)
	}
}
