package main

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	accessapp "github.com/qianlan33333-png/AI-CRM-v3/internal/access/app"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/access/credential"
	accesshttp "github.com/qianlan33333-png/AI-CRM-v3/internal/access/http"
	accessstore "github.com/qianlan33333-png/AI-CRM-v3/internal/access/store"
	adminopsapp "github.com/qianlan33333-png/AI-CRM-v3/internal/adminops/app"
	adminopsport "github.com/qianlan33333-png/AI-CRM-v3/internal/adminops/port"
	adminopsstore "github.com/qianlan33333-png/AI-CRM-v3/internal/adminops/store"
	aiassistant "github.com/qianlan33333-png/AI-CRM-v3/internal/aiassistant"
	aiassistantapp "github.com/qianlan33333-png/AI-CRM-v3/internal/aiassistant/app"
	aiassistanthttp "github.com/qianlan33333-png/AI-CRM-v3/internal/aiassistant/http"
	aiassistantstore "github.com/qianlan33333-png/AI-CRM-v3/internal/aiassistant/store"
	automation "github.com/qianlan33333-png/AI-CRM-v3/internal/automation"
	automationapp "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/app"
	automationstore "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/store"
	channelstore "github.com/qianlan33333-png/AI-CRM-v3/internal/channel"
	configapp "github.com/qianlan33333-png/AI-CRM-v3/internal/config/app"
	configmodule "github.com/qianlan33333-png/AI-CRM-v3/internal/config/module"
	configstore "github.com/qianlan33333-png/AI-CRM-v3/internal/config/store"
	coupon "github.com/qianlan33333-png/AI-CRM-v3/internal/coupon"
	couponapp "github.com/qianlan33333-png/AI-CRM-v3/internal/coupon/app"
	couponstore "github.com/qianlan33333-png/AI-CRM-v3/internal/coupon/store"
	customerapp "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/app"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	customerhttp "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/http"
	customerstore "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/store"
	externaleffects "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	groupops "github.com/qianlan33333-png/AI-CRM-v3/internal/groupops"
	groupopsapp "github.com/qianlan33333-png/AI-CRM-v3/internal/groupops/app"
	groupopsstore "github.com/qianlan33333-png/AI-CRM-v3/internal/groupops/store"
	hxcapp "github.com/qianlan33333-png/AI-CRM-v3/internal/hxcdashboard/app"
	hxchttp "github.com/qianlan33333-png/AI-CRM-v3/internal/hxcdashboard/http"
	hxcprovider "github.com/qianlan33333-png/AI-CRM-v3/internal/hxcdashboard/provider"
	hxcstore "github.com/qianlan33333-png/AI-CRM-v3/internal/hxcdashboard/store"
	hxcworker "github.com/qianlan33333-png/AI-CRM-v3/internal/hxcdashboard/worker"
	identityapp "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/app"
	identityhttp "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/http"
	identityprovider "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/provider"
	identityquery "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/query"
	identitysecure "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/secure"
	identitystore "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/store"
	media "github.com/qianlan33333-png/AI-CRM-v3/internal/media"
	mediaapp "github.com/qianlan33333-png/AI-CRM-v3/internal/media/app"
	groupopsmaterial "github.com/qianlan33333-png/AI-CRM-v3/internal/media/groupopsmaterial"
	mediastore "github.com/qianlan33333-png/AI-CRM-v3/internal/media/store"
	operationcycle "github.com/qianlan33333-png/AI-CRM-v3/internal/operationcycle"
	operationapp "github.com/qianlan33333-png/AI-CRM-v3/internal/operationcycle/app"
	operationstore "github.com/qianlan33333-png/AI-CRM-v3/internal/operationcycle/store"
	orderui "github.com/qianlan33333-png/AI-CRM-v3/internal/order"
	orderapp "github.com/qianlan33333-png/AI-CRM-v3/internal/order/app"
	orderhttp "github.com/qianlan33333-png/AI-CRM-v3/internal/order/http"
	ordermigration "github.com/qianlan33333-png/AI-CRM-v3/internal/order/migration"
	orderstore "github.com/qianlan33333-png/AI-CRM-v3/internal/order/store"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/outbound"
	payment "github.com/qianlan33333-png/AI-CRM-v3/internal/payment"
	paymentapp "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/app"
	paymenthttp "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/http"
	paymentprovider "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/provider"
	paymentsession "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/session"
	paymentstore "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/store"
	platformaudit "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/audit"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformjobqueue "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/jobqueue"
	platformoutbox "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/outbox"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	platformruntime "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/runtime"
	platformwebhook "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/webhook"
	productmodule "github.com/qianlan33333-png/AI-CRM-v3/internal/product"
	productapp "github.com/qianlan33333-png/AI-CRM-v3/internal/product/app"
	productstore "github.com/qianlan33333-png/AI-CRM-v3/internal/product/store"
	releaseapp "github.com/qianlan33333-png/AI-CRM-v3/internal/release/app"
	releaseport "github.com/qianlan33333-png/AI-CRM-v3/internal/release/port"
	segment "github.com/qianlan33333-png/AI-CRM-v3/internal/segment"
	segmentadapter "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/adapter"
	segmentapp "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/app"
	segmentcompiler "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/compiler"
	segmenthttp "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/http"
	segmentstore "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/store"
	surveymodule "github.com/qianlan33333-png/AI-CRM-v3/internal/survey"
	surveyapp "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/app"
	surveyprovider "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/provider"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/survey/secure"
	surveystore "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/store"
	tag "github.com/qianlan33333-png/AI-CRM-v3/internal/tag"
	tagapp "github.com/qianlan33333-png/AI-CRM-v3/internal/tag/app"
	tagstore "github.com/qianlan33333-png/AI-CRM-v3/internal/tag/store"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/webshell"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/wecom"
	wecomadapter "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/adapter"
	"github.com/riverqueue/river"
)

type composedApplication struct {
	pool           *platformpostgres.Pool
	handler        http.Handler
	management     *accessapp.Management
	weComProcessor wecom.InboxProcessor
	effectsRuntime *platformjobqueue.Runtime
	customerSync   wecom.CustomerSyncService
	adminOps       *adminopsapp.ProjectionService
	release        *releaseapp.ObservationService
	diagnostics    *adminopsapp.DiagnosticsService
	hxcDashboard   hxcapp.Service
	hxcSource      *hxcprovider.MySQL
}

func compose(ctx context.Context, cfg platformconfig.Runtime) (*composedApplication, error) {
	var hxcSource *hxcprovider.MySQL
	pool, err := platformpostgres.Open(ctx, platformpostgres.Config{URL: cfg.DatabaseURL, MaxConnections: 20, MinConnections: 1})
	if err != nil {
		return nil, err
	}
	fail := func(err error) (*composedApplication, error) {
		if hxcSource != nil {
			_ = hxcSource.Close()
		}
		pool.Close()
		return nil, err
	}
	uow, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		return fail(err)
	}
	auditService, err := platformaudit.NewService(platformaudit.NewPostgreSQLStore())
	if err != nil {
		return fail(err)
	}
	inboxService, err := platformwebhook.NewService(platformwebhook.NewPostgreSQLStore())
	if err != nil {
		return fail(err)
	}

	passwords := credential.PasswordHasher{}
	dummyHash, err := passwords.Hash("aicrm-dummy-password-never-valid")
	if err != nil {
		return fail(err)
	}
	accessRepository := accessstore.NewPostgreSQL()
	authentication, err := accessapp.NewAuthentication(accessRepository, uow, passwords, accessapp.AuthenticationConfig{DummyPHCHash: dummyHash})
	if err != nil {
		return fail(err)
	}
	management, err := accessapp.NewManagement(accessRepository, uow, passwords, nil)
	if err != nil {
		return fail(err)
	}

	if cfg.Survey.IdentityPhoneDataKey == "" {
		return fail(errors.New("identity phone data encryption key is not configured"))
	}
	phoneVault, err := identitysecure.NewPhoneVault(cfg.Survey.IdentityPhoneDataKey)
	if err != nil {
		return fail(err)
	}
	oneID := identityapp.OneIDService{Store: identitystore.NewPostgresStore(phoneVault)}
	queries := identityquery.NewPostgreSQL(phoneVault)
	paymentRepository := paymentstore.NewPostgreSQL()
	customerStore := customerstore.NewPostgreSQL()
	requestSecurity := requestAccessSecurity{authentication: authentication}
	effectsModule := externaleffects.NewModuleRegistration()
	effectWorkers := river.NewWorkers()
	if err = effectsModule.RegisterWorkers(effectWorkers); err != nil {
		return fail(err)
	}
	customerSyncWorker := wecom.NewCustomerSyncWorker()
	if err = river.AddWorkerSafely[wecom.CustomerSyncJobArgs](effectWorkers, customerSyncWorker); err != nil {
		return fail(err)
	}
	hxcDashboardWorker := &hxcworker.Worker{}
	if err = river.AddWorkerSafely[hxcworker.Args](effectWorkers, hxcDashboardWorker); err != nil {
		return fail(err)
	}
	paymentReconciliationWorker := payment.NewReconciliationWorker()
	if err = river.AddWorkerSafely[payment.ReconciliationJobArgs](effectWorkers, paymentReconciliationWorker); err != nil {
		return fail(err)
	}
	audienceRefreshWorker := segment.NewAudienceRefreshWorker()
	if err = river.AddWorkerSafely[segment.AudienceRefreshJobArgs](effectWorkers, audienceRefreshWorker); err != nil {
		return fail(err)
	}
	audienceMemberEventWorker := segment.NewAudienceMemberEventDispatchWorker()
	if err = river.AddWorkerSafely[segment.AudienceMemberEventDispatchJobArgs](effectWorkers, audienceMemberEventWorker); err != nil {
		return fail(err)
	}
	audienceScheduleWorker := segment.NewAudienceScheduleScanWorker()
	if err = river.AddWorkerSafely[segment.AudienceScheduleScanJobArgs](effectWorkers, audienceScheduleWorker); err != nil {
		return fail(err)
	}
	effectClient, err := platformjobqueue.NewInsertClient(pool.Native(), effectWorkers)
	if err != nil {
		return fail(err)
	}
	paymentReconciliationEnqueuer, err := payment.NewRiverReconciliationEnqueuer(effectClient)
	if err != nil {
		return fail(err)
	}
	customerSyncEnqueuer, err := wecom.NewRiverCustomerSyncEnqueuer(effectClient)
	if err != nil {
		return fail(err)
	}
	hxcEnqueuer, err := hxcworker.NewEnqueuer(effectClient)
	if err != nil {
		return fail(err)
	}
	effectRepository, err := externaleffects.NewRepository(pool.Native(), effectClient)
	if err != nil {
		return fail(err)
	}
	effectsRuntime, err := platformjobqueue.NewRuntimeWithPeriodic(pool.Native(), effectWorkers, []*river.PeriodicJob{segment.AudienceSchedulePeriodicJob()}, platformjobqueue.OutboundQueue, wecom.CustomerSyncQueue, payment.ReconciliationQueue, hxcworker.Queue, segment.AudienceRefreshQueue)
	if err != nil {
		return fail(err)
	}
	effectsBindings, err := effectsModule.Bind(effectRepository, requestSecurity)
	if err != nil {
		return fail(err)
	}
	mediaModule := media.NewModuleRegistration()
	mediaRepository, err := mediastore.NewPostgreSQL(pool.Native(), uow)
	if err != nil {
		return fail(err)
	}
	aiModule := aiassistant.NewModuleRegistration()
	aiRepository, err := aiassistantstore.NewPostgreSQL(pool.Native(), uow)
	if err != nil {
		return fail(err)
	}
	aiCustomers := aiCustomerSnapshotAdapter{read: func(ctx context.Context, id customerdomain.CustomerID) (customerdomain.CustomerID, customerdomain.Status, string, string, error) {
		detail, readErr := customerStore.Detail(ctx, id)
		return detail.CustomerID, detail.CustomerStatus, detail.DisplayName, detail.OneIDLabel, readErr
	}}
	aiService, err := aiassistantapp.NewService(uow, aiRepository, aiCustomers, aiStaffSnapshotAdapter{repository: accessRepository}, aiMaterialAdapter{capturer: mediaRepository, references: mediaRepository}, oneID)
	if err != nil {
		return fail(err)
	}
	privateWriter, err := outbound.NewPrivateMessageRepository(pool.Native(), effectRepository)
	if err != nil {
		return fail(err)
	}
	if err = aiService.BindOutbound(privateWriter, cfg.AIAssistant.DispatchEnabled); err != nil {
		return fail(err)
	}
	if err = aiService.BindReconciler(effectRepository); err != nil {
		return fail(err)
	}
	aiHandler, err := aiassistanthttp.NewHandler(aiassistanthttp.Config{Application: aiService, Security: requestSecurity, Authorizer: accessapp.AIAssistantAuthorizer{}, Integration: aiassistanthttp.IntegrationConfig{Enabled: cfg.AIAssistant.IntakeEnabled, Key: cfg.AIAssistant.IntegrationKey, Secret: cfg.AIAssistant.IntegrationSecret, ActorID: cfg.AIAssistant.IntegrationActorID}, DispatchReady: cfg.AIAssistant.DispatchEnabled})
	if err != nil {
		return fail(err)
	}
	contentDelivery := mediaapp.NewContentDeliveryService(uow, mediaRepository)
	mediaService, err := mediaapp.NewHTTPFacade(mediaRepository)
	if err != nil {
		return fail(err)
	}
	mediaBindings, err := mediaModule.Bind(mediaService, requestSecurity)
	if err != nil {
		return fail(err)
	}
	mediaContentBindings, err := mediaModule.BindContentDelivery(contentDelivery, mediaRepository)
	if err != nil {
		return fail(err)
	}
	automationModule := automation.NewModuleRegistration()
	automationRepository, err := automationstore.NewPostgreSQL(pool.Native(), uow)
	if err != nil {
		return fail(err)
	}
	outboundMessages, err := outbound.NewMessageService(pool.Native(), uow, effectRepository, automationRepository)
	if err != nil {
		return fail(err)
	}
	automationService := automationapp.NewAgentServiceWithMediaReferences(uow, automationRepository, mediaRepository, mediaRepository, mediaRepository, mediaRepository, automationRepository)
	automationBindings, err := automationModule.Bind(automationService, requestSecurity)
	if err != nil {
		return fail(err)
	}
	segmentModule := segment.NewModuleRegistration()
	segmentRepository, err := segmentstore.NewPostgreSQL(pool.Native(), uow)
	if err != nil {
		return fail(err)
	}
	segmentService := segmentapp.NewService(uow, segmentRepository)
	segmentEvaluator, err := segmentapp.NewEvaluator(segmentcompiler.Compiler{}, segmentadapter.CustomerSource{UoW: uow, Customers: customerStore}, segmentadapter.CanonicalCustomers{UoW: uow, Resolver: canonicalCustomerAdapter{reader: queries}})
	if err != nil {
		return fail(err)
	}
	segmentEnqueuer, err := segment.NewRiverRefreshEnqueuer(effectClient)
	if err != nil {
		return fail(err)
	}
	segmentMemberEventEnqueuer, err := segment.NewRiverMemberEventEnqueuer(effectClient)
	if err != nil {
		return fail(err)
	}
	segmentSnapshots, err := segmentapp.NewSnapshotService(uow, segmentRepository, segmentEvaluator, segmentEnqueuer, segmentMemberEventEnqueuer)
	if err != nil {
		return fail(err)
	}
	if err = audienceRefreshWorker.BindService(segmentSnapshots); err != nil {
		return fail(err)
	}
	scheduledRefreshes, err := segmentapp.NewScheduledRefreshService(uow, segmentRepository, segmentSnapshots)
	if err != nil {
		return fail(err)
	}
	if err = audienceScheduleWorker.BindService(scheduledRefreshes); err != nil {
		return fail(err)
	}
	segmentStaff := automationOpsStaffAdapter{uow: uow, users: accessRepository}
	automationProviderReady := cfg.Effects.ProviderEnabled && cfg.WeCom.Enabled && cfg.AutomationOperations.ProviderEnabled()
	segmentExecution, err := segmentapp.NewExecutionService(uow, segmentRepository, automationService, segmentStaff, automationProviderReady)
	if err != nil {
		return fail(err)
	}
	automationRecipientLimit := cfg.AutomationOperations.MaxRecipientsPerRun
	if automationRecipientLimit == 0 {
		automationRecipientLimit = 1
	}
	automationRuntime, err := automationapp.NewRuntimeService(uow, automationRepository, segmentExecution, segmentSnapshots, automationRecipientLimit)
	if err != nil {
		return fail(err)
	}
	if err = automationRuntime.SetMessageAccepter(outboundMessages); err != nil {
		return fail(err)
	}
	if err = automationRuntime.SetEffectReconciler(effectRepository); err != nil {
		return fail(err)
	}
	if err = audienceMemberEventWorker.Bind(segmentSnapshots, automationMemberEventSink{runtime: automationRuntime}); err != nil {
		return fail(err)
	}
	automationBindings.Runtime, err = automationModule.BindRuntime(automationRuntime, requestSecurity)
	if err != nil {
		return fail(err)
	}
	segmentRuntime := segmentapp.NewRuntimeFacade(segmentService, segmentSnapshots, segmentExecution)
	segmentBindings, err := segmentModule.BindRuntime(segmentRuntime, segmentRuntime, requestSecurity)
	if err != nil {
		return fail(err)
	}
	segmentWebhookService, err := segmentapp.NewWebhookService(uow, segmentRepository, oneID, segmentSnapshots)
	if err != nil {
		return fail(err)
	}
	segmentWebhookHandler, err := segmenthttp.NewWebhookHandler(segmentWebhookService, cfg.AutomationOperations.WebhookSecret)
	if err != nil {
		return fail(err)
	}
	mediaPreparationWriter := mediaapp.NewGroupOpsMaterialPreparationWriter(uow, mediaRepository)
	mediaPreparationBindings, err := mediaModule.BindMaterialPreparation(mediaRepository, mediaPreparationWriter)
	if err != nil {
		return fail(err)
	}
	groupOpsProvider, err := outbound.NewGroupMessageProvider(outbound.GroupMessageProviderConfig{
		Enabled:           cfg.Effects.ProviderEnabled,
		PreparationWriter: mediaPreparationBindings.Writer,
	})
	if err != nil {
		return fail(err)
	}
	materialFreezer, err := groupopsmaterial.NewFreezer(mediaPreparedPlanReader{reader: mediaPreparationBindings.Reader})
	if err != nil {
		return fail(err)
	}
	groupOpsMaterials, err := newGroupOpsMaterialAdapter(mediaContentBindings.SourceCapturer, materialFreezer)
	if err != nil {
		return fail(err)
	}
	tagModule := tag.NewModuleRegistration()
	var callbackStateDigester wecom.StateDigester
	if cfg.WeCom.CallbackEnabled {
		callbackStateDigester, err = wecom.NewHMACStateDigester([]byte(cfg.WeCom.ChannelStateHMACKey))
		if err != nil {
			return fail(err)
		}
	}
	channelAssetStore := channelstore.NewPostgreSQLAssetStore(pool.Native())
	channelAcquisition := channelstore.NewPostgreSQLStore()
	channelEntrantActions := channelstore.NewEntrantActionStore(effectRepository, channelWelcomeMaterialAdapter{resolver: groupOpsMaterials})
	channelLinkStore := channelstore.NewAcquisitionLinkStore()
	channelAssetCompletionSink, err := outbound.NewChannelAssetCompletionSink(channelAssetCompletionAdapter{assets: channelAssetStore, bindings: channelAcquisition, digester: callbackStateDigester, corpID: cfg.WeCom.CorpID})
	if err != nil {
		return fail(err)
	}
	channelEntrantCompletionSink, err := outbound.NewChannelEntrantCompletionSink(channelEntrantActions)
	if err != nil {
		return fail(err)
	}
	channelLinkCompletionSink, err := outbound.NewChannelLinkCompletionSink(channelLinkStore)
	if err != nil {
		return fail(err)
	}
	tagRepository, err := tagstore.NewPostgreSQL(pool.Native(), uow)
	if err != nil {
		return fail(err)
	}
	tagCompletionSink, err := outbound.NewTagCatalogCompletionSink(tagRepository)
	if err != nil {
		return fail(err)
	}
	groupOpsRepository, err := groupopsstore.NewPostgreSQL(pool.Native(), uow)
	if err != nil {
		return fail(err)
	}
	groupOpsStaff := groupOpsStaffAdapter{access: accessRepository, owners: groupOpsRepository}
	groupOpsDirectory := providerDisabledGroupOpsDirectory{}
	groupOpsEvidence := providerDisabledGroupOpsEvidence{}
	groupOpsService := groupopsapp.NewService(uow, groupOpsRepository, groupOpsStaff, groupOpsRepository)
	groupOpsHistory := groupopsapp.NewHistoryService(uow, groupOpsRepository)
	groupOpsRuntime := groupopsapp.NewRuntimeService(uow, groupOpsRepository, groupOpsRepository, effectRepository, groupOpsStaff, groupOpsDirectory, groupOpsStaff, groupOpsEvidence, groupOpsExternalReconciler{repository: effectRepository}, groupOpsMaterials)
	groupOpsRuntime.SetDispatchEnabled(true)
	groupOpsProtocols := &groupOpsProtocolAuthenticator{key: []byte(cfg.GroupOps.WebhookSecret), replay: groupOpsRepository, now: time.Now}
	groupOpsModule := groupops.NewModuleRegistration()
	groupOpsBindings, err := groupOpsModule.BindWithHistory(groupOpsService, groupOpsRuntime, groupOpsHistory, requestSecurity, groupOpsProtocols, mediaContentBindings.ContentDelivery)
	if err != nil {
		return fail(err)
	}
	groupOpsCompletionSink, err := outbound.NewGroupMessageCompletionSink(groupOpsRepository)
	if err != nil {
		return fail(err)
	}
	privateCompletionSink, err := outbound.NewPrivateMessageCompletionSink(privateWriter, aiRepository)
	if err != nil {
		return fail(err)
	}
	outboundCompletionSink, err := outbound.NewCompletionRouterWithAllChannels(tagCompletionSink, groupOpsCompletionSink, channelAssetCompletionSink, channelEntrantCompletionSink, channelLinkCompletionSink)
	if err != nil {
		return fail(err)
	}
	outboundCompletionSink.WithPrivateMessage(privateCompletionSink)
	outboundCompletionSink.WithAutomationMessage(outboundMessages)
	if cfg.Survey.DataKey == "" {
		return fail(errors.New("survey data encryption key is not configured"))
	}
	surveyCipher, err := secure.NewCipher(cfg.Survey.DataKey)
	if err != nil {
		return fail(err)
	}
	surveyRepository, err := surveystore.NewPostgreSQL(pool.Native(), uow, surveyCipher)
	if err != nil {
		return fail(err)
	}
	surveyDefinitions := surveyapp.NewService(uow, surveyRepository)
	surveySubmissions := surveyapp.NewSubmissionService(uow, surveyRepository, surveyCipher)
	if err = surveySubmissions.BindCustomerTimeline(customerStore); err != nil {
		return fail(err)
	}
	if err = surveySubmissions.BindDeclaredPhone(oneID, customerStore); err != nil {
		return fail(err)
	}
	surveyOAuthProvider, err := surveyprovider.NewWeChatOAuth(cfg.Survey.OAuthEnabled, cfg.Survey.OAuthAppID, cfg.Survey.OAuthSecret, cfg.Survey.OAuthOpenPlatformID, cfg.PublicOrigin+"/api/h5/surveys/oauth/callback", cfg.Survey.OAuthScope)
	if err != nil {
		return fail(err)
	}
	surveyOAuth := surveyapp.NewOAuthService(uow, surveyRepository, surveyOAuthProvider, oneID)
	surveyModule := surveymodule.NewModuleRegistration()
	surveyBindings, err := surveyModule.Bind(surveyDefinitions, surveySubmissions, requestSecurity, surveyOAuth)
	if err != nil {
		return fail(err)
	}
	tagCatalog := tagapp.NewCatalogService(uow, tagRepository, tagRepository, tagRepository, tagRepository)
	tagOutbound, err := outbound.NewTagCatalogSyncAccepter(effectRepository)
	if err != nil {
		return fail(err)
	}
	tagSync := tagapp.NewSyncService(uow, tagRepository, tagRepository, tagOutbound)
	tagGate := tagapp.NewExecutionStatusService(uow, tagRepository)
	tagBindings, err := tagModule.Bind(tagCatalog, tagSync, tagGate, requestSecurity)
	if err != nil {
		return fail(err)
	}
	productModule := productmodule.NewModuleRegistration()
	productRepository, err := productstore.NewPostgreSQL(pool.Native(), uow)
	if err != nil {
		return fail(err)
	}
	productEvents, err := productstore.NewTransactionalEventAppender(auditService, platformoutbox.NewPostgreSQL())
	if err != nil {
		return fail(err)
	}
	productCatalog := productapp.NewService(uow, productRepository, productEvents)
	productLifecycle := productapp.NewLocalProductLifecycleService(uow, productRepository, productEvents)
	productServicePeriod := productapp.NewServicePeriodService(uow, productRepository, productEvents)
	productExternalPush, err := productapp.NewCommerceExternalPushService(uow, productRepository, productstore.NewLocalExternalPushEffectAccepter(), productEvents)
	if err != nil {
		return fail(err)
	}
	productBindings, err := productModule.Bind(productCatalog, productLifecycle, productServicePeriod, productExternalPush, requestSecurity)
	if err != nil {
		return fail(err)
	}
	productTargets, err := productapp.NewTargetReader(productCatalog, productServicePeriod)
	if err != nil {
		return fail(err)
	}
	couponModule := coupon.NewModuleRegistration()
	couponRepository, err := couponstore.NewPostgreSQL(pool.Native(), uow)
	if err != nil {
		return fail(err)
	}
	couponService := couponapp.NewService(uow, couponRepository, productTargets, couponRepository)
	couponBindings, err := couponModule.Bind(couponService, productCatalog, requestSecurity)
	if err != nil {
		return fail(err)
	}
	// PR09 config has no OneID, Provider-write, or worker dependency. Its
	// local settings, audit rows, and idempotency receipts share this UOW.
	configModule := configmodule.NewRegistration()
	configRepository, err := configstore.NewPostgreSQL(pool.Native(), uow)
	if err != nil {
		return fail(err)
	}
	configManager := configapp.NewManager(uow, configRepository, configRepository)
	settingsService := configapp.NewSettingsCompatibilityService(uow, configRepository, configManager, configapp.SecretConfiguredSnapshot{
		DatabaseURL: cfg.DatabaseURL != "", WeComSecret: cfg.WeCom.Secret != "",
		WeComCallbackToken: cfg.WeCom.CallbackToken != "", WeComCallbackAESKey: cfg.WeCom.CallbackAESKey != "",
	})
	setupWizard, err := configapp.NewSetupWizardService(configManager, configapp.SetupWizardSecretConfigured{
		WeComSecret: cfg.WeCom.Secret != "", WeComCallbackToken: cfg.WeCom.CallbackToken != "", WeComCallbackAESKey: cfg.WeCom.CallbackAESKey != "",
	})
	if err != nil {
		return fail(err)
	}
	adminOpsProjectionStore, err := adminopsstore.NewProjectionPostgreSQL(pool.Native(), uow)
	if err != nil {
		return fail(err)
	}
	adminOpsProjection, err := adminopsapp.NewProjectionService(uow, adminOpsProjectionStore)
	if err != nil {
		return fail(err)
	}
	diagnostics, err := adminopsapp.NewDiagnosticsService(adminOpsProjection)
	if err != nil {
		return fail(err)
	}
	releaseObservation, err := releaseapp.NewObservationService(adminOpsReleaseObservationWriter{projections: adminOpsProjection})
	if err != nil {
		return fail(err)
	}
	configBindings, err := configModule.Bind(settingsService, setupWizard, configManager, adminOpsProjection, requestSecurity)
	if err != nil {
		return fail(err)
	}
	channelCursorKey := make([]byte, 32)
	if _, err = rand.Read(channelCursorKey); err != nil {
		return fail(err)
	}
	channelCatalogStore := channelstore.NewPostgreSQLCatalogStore()
	channelModule := channelstore.NewModuleRegistration()
	channelEvents, err := channelstore.NewChannelCatalogEventAppender(auditService, platformoutbox.NewPostgreSQL())
	if err != nil {
		return fail(err)
	}
	channelCatalogService := channelstore.NewCatalogService(uow, channelCatalogStore, channelCatalogStore, channelEvents,
		channelMaterialReferenceAdapter{media: mediaRepository}, channelTagReferenceAdapter{tags: tagRepository}, channelStaffReferenceAdapter{users: accessRepository})
	channelCatalog, err := channelstore.NewCatalogHTTPHandler(channelstore.CatalogHTTPConfig{Application: channelCatalogService, Security: requestSecurity, CursorSigningKey: channelCursorKey})
	if err != nil {
		return fail(err)
	}
	channelAssetService := channelstore.NewAssetService(uow, channelCatalogStore, channelAssetStore, effectRepository, channelEvents)
	if err = channelAssetService.SetReconciler(channelAssetReconciler{uow: uow, assets: channelAssetStore, effects: effectRepository, bindings: channelAcquisition, digester: callbackStateDigester, corpID: cfg.WeCom.CorpID}); err != nil {
		return fail(err)
	}
	channelAssetHandler, err := channelstore.NewAssetHTTPHandler(channelAssetService, requestSecurity)
	if err != nil {
		return fail(err)
	}
	operationModule := operationcycle.NewModuleRegistration()
	operationRepository := operationstore.NewRepository()
	operationJournal := operationstore.NewEventJournal()
	operationService := operationapp.NewService(uow, operationRepository, operationJournal, operationJournal)
	operationBindings, err := operationModule.Bind(operationService, requestSecurity, cfg.OperationCycleServiceToken)
	if err != nil {
		return fail(err)
	}
	oneIDHandler, err := identityhttp.NewHandler(identityhttp.Config{
		UnitOfWork: uow, Authenticator: requestSecurity, CSRF: requestSecurity,
		OneID: oneID, Queries: queries, Audit: auditService,
	})
	if err != nil {
		return fail(err)
	}
	cursorSigningKey := make([]byte, 32)
	if _, err = rand.Read(cursorSigningKey); err != nil {
		return fail(err)
	}
	customerProfileStore := wecom.NewPostgreSQLCustomerSyncStore()
	customerHandler, err := customerhttp.NewHandler(customerhttp.Config{UnitOfWork: uow, Auth: requestSecurity, CSRF: requestSecurity,
		Directory: customerapp.Directory{Store: customerStore, SigningKey: cursorSigningKey}, Store: customerStore, Identities: queries, Audit: auditService,
		Canonical: canonicalCustomerAdapter{reader: queries},
		Owners:    customerOwnerAdapter{uow: uow, observations: customerProfileStore, users: accessRepository},
		Tags:      customerTagAdapter{uow: uow, observations: customerProfileStore, names: tagRepository},
		Surveys:   customerSurveyAdapter{reader: surveySubmissions},
		Timeline:  customerTimelineAdapter{uow: uow, reader: customerStore}, Chat: disabledCustomerChatActivity{}, ProfileSigningKey: cursorSigningKey})
	if err != nil {
		return fail(err)
	}
	orderRepository, err := orderstore.NewPostgreSQL(pool.Native(), uow)
	if err != nil {
		return fail(err)
	}
	orderService := orderapp.NewService(uow, orderRepository)
	orderHandler, err := orderhttp.NewHandler(orderService, requestSecurity, orderCustomerDisplayNameAdapter{uow: uow, reader: customerStore})
	if err != nil {
		return fail(err)
	}
	orderRuns := ordermigration.PostgreSQLRuns{Pool: pool.Native()}
	orderImportHandler, err := orderhttp.NewImportHandler(ordermigration.OrderOnlyRunner{Orders: orderService, Runs: orderRuns}, orderRuns, requestSecurity)
	if err != nil {
		return fail(err)
	}
	paymentSession, err := paymentsession.NewService(uow, oneID, paymentsession.NewPostgreSQL(), 10*time.Minute)
	if err != nil {
		return fail(err)
	}
	paymentService := paymentapp.NewService(uow, paymentRepository, orderService, paymentSession, effectRepository, effectRepository)
	if err = paymentService.SetCheckoutProductReader(productTargets); err != nil {
		return fail(err)
	}
	if err = paymentService.SetReconciliationEnqueuer(paymentReconciliationEnqueuer); err != nil {
		return fail(err)
	}
	paymentCompletionSink, err := payment.NewCompletionSink(paymentRepository, paymentReconciliationEnqueuer, orderService)
	if err != nil {
		return fail(err)
	}
	if err = effectRepository.SetCompletionSink(composedCompletionRouter{outbound: outboundCompletionSink, payment: paymentCompletionSink}); err != nil {
		return fail(err)
	}
	if err = paymentReconciliationWorker.BindService(paymentService); err != nil {
		return fail(err)
	}
	var wechatPayAdapter *paymentprovider.WeChatPay
	var paymentCallbackVerifier *paymentprovider.CallbackVerifier
	if cfg.WeChatPay.Enabled {
		privateKey, readErr := os.ReadFile(cfg.WeChatPay.PrivateKeyPath)
		if readErr != nil {
			return fail(readErr)
		}
		platformCertificate, readErr := os.ReadFile(cfg.WeChatPay.PlatformCertPath)
		if readErr != nil {
			return fail(readErr)
		}
		signer, parseErr := paymentprovider.ParseMerchantPrivateKey(privateKey)
		if parseErr != nil {
			return fail(parseErr)
		}
		platformSerial, platformKey, parseErr := paymentprovider.ParsePlatformCertificate(platformCertificate)
		if parseErr != nil {
			return fail(parseErr)
		}
		credential := paymentprovider.Credential{MerchantID: cfg.WeChatPay.MerchantID, Serial: cfg.WeChatPay.MerchantSerial, Signer: signer, PlatformKeys: map[string]*rsa.PublicKey{platformSerial: platformKey}}
		loader := paymentprovider.DBMaterialLoader{UOW: uow, Intents: paymentRepository, Identities: queries, AppScope: cfg.WeChatPay.AppScope}
		wechatPayAdapter, err = paymentprovider.NewWeChatPay(paymentprovider.Config{Enabled: true, AppID: cfg.WeChatPay.AppID, AppScope: cfg.WeChatPay.AppScope, APIBaseURL: "https://api.mch.weixin.qq.com", PaymentNotifyURL: cfg.PublicOrigin + "/api/public/wechat-pay/callbacks/payment", RefundNotifyURL: cfg.PublicOrigin + "/api/public/wechat-pay/callbacks/refund", Credential: credential}, loader, &http.Client{Timeout: 10 * time.Second})
		if err != nil {
			return fail(err)
		}
		paymentCallbackVerifier, err = paymentprovider.NewCallbackVerifier(credential.PlatformKeys, []byte(cfg.WeChatPay.APIV3Key), cfg.WeChatPay.AppID, cfg.WeChatPay.MerchantID)
		if err != nil {
			return fail(err)
		}
	} else {
		wechatPayAdapter, err = paymentprovider.NewWeChatPay(paymentprovider.Config{}, nil, nil)
		if err != nil {
			return fail(err)
		}
	}
	shopLoader := paymentprovider.DBMaterialLoader{UOW: uow, Intents: paymentRepository}
	wechatShopAdapter, err := paymentprovider.NewWeChatShop(paymentprovider.ShopConfig{Enabled: cfg.WeChatShop.Enabled, AppID: cfg.WeChatShop.AppID, AppSecret: cfg.WeChatShop.AppSecret, APIBaseURL: "https://api.weixin.qq.com"}, shopLoader, &http.Client{Timeout: 10 * time.Second})
	if err != nil {
		return fail(err)
	}
	if err = paymentService.SetShopReconciler(wechatShopAdapter); err != nil {
		return fail(err)
	}
	if err = paymentService.SetWeChatPayReconciler(wechatPayAdapter); err != nil {
		return fail(err)
	}
	paymentAdapter := paymentProviderRouter{wechatPay: wechatPayAdapter, wechatShop: wechatShopAdapter}
	paymentHandler, err := paymenthttp.NewHandler(paymentService, paymentCallbackVerifier, requestSecurity, cfg.WeChatPay.Enabled, cfg.WeChatShop.Enabled)
	if err != nil {
		return fail(err)
	}
	if cfg.WeChatPay.Enabled {
		miniProgramVerifier, verifyErr := identityprovider.NewWeChatMiniProgram(identityprovider.WeChatMiniProgramConfig{AppID: cfg.WeChatPay.AppID, AppSecret: cfg.WeChatPay.AppSecret, APIBaseURL: "https://api.weixin.qq.com"}, &http.Client{Timeout: 10 * time.Second})
		if verifyErr != nil {
			return fail(verifyErr)
		}
		if err = paymentHandler.SetTrustedSessionIssuer(miniProgramVerifier, paymentSession); err != nil {
			return fail(err)
		}
	}
	if cfg.WeChatShop.Enabled {
		shopCredential, credentialErr := paymentprovider.NewShopCallbackCredential(cfg.WeChatShop.AppID, cfg.WeChatShop.CallbackToken, cfg.WeChatShop.CallbackEncodingAESKey)
		if credentialErr != nil {
			return fail(credentialErr)
		}
		shopVerifier, verifierErr := paymentprovider.NewShopCallbackVerifier(shopCredential)
		if verifierErr != nil {
			return fail(verifierErr)
		}
		if err = paymentHandler.SetShopCallbackVerifier(shopVerifier); err != nil {
			return fail(err)
		}
	}

	renderer, err := webshell.NewRenderer()
	if err != nil {
		return fail(err)
	}
	accessHandler, err := accesshttp.NewHandler(accesshttp.Config{
		Renderer: renderer, Auth: authentication, Management: management, CookieSecure: true, CookiePath: "/",
	})
	if err != nil {
		return fail(err)
	}
	shellHandler, err := webshell.NewHandler(webshell.HandlerOptions{Renderer: renderer})
	if err != nil {
		return fail(err)
	}

	providerClient, err := wecomadapter.New(wecomadapter.Config{
		Enabled: cfg.WeCom.Enabled, CorpID: cfg.WeCom.CorpID, AgentID: cfg.WeCom.AgentID, Secret: cfg.WeCom.Secret, ContactSecret: cfg.WeCom.ContactSecret,
		AdminCallbackURI: cfg.PublicOrigin + "/auth/wecom/callback", SidebarCallbackURI: cfg.PublicOrigin + "/api/sidebar/oauth/callback",
	})
	if err != nil {
		return fail(err)
	}
	if err = channelAssetService.SetProvider(providerClient); err != nil {
		return fail(err)
	}
	channelAcquisitionService := channelstore.NewAcquisitionService(uow, channelCatalogService, channelStaffReferenceAdapter{users: accessRepository}, providerClient)
	channelAcquisitionHandler, err := channelstore.NewAcquisitionHTTPHandler(channelAcquisitionService, requestSecurity)
	if err != nil {
		return fail(err)
	}
	channelHistoryService := channelstore.NewHistoryService(uow, channelCatalogService, channelstore.NewPostgreSQLStore())
	channelHistoryHandler, err := channelstore.NewHistoryHTTPHandler(channelHistoryService, requestSecurity, channelCursorKey)
	if err != nil {
		return fail(err)
	}
	channelLinkService := channelstore.NewAcquisitionLinkService(uow, channelLinkStore, effectRepository)
	if err = channelLinkService.SetProvider(providerClient); err != nil {
		return fail(err)
	}
	if err = channelLinkService.SetReconciler(channelLinkReconciler{uow: uow, store: channelLinkStore, effects: effectRepository, provider: providerClient}); err != nil {
		return fail(err)
	}
	channelLinkHandler, err := channelstore.NewAcquisitionLinkHTTPHandler(channelLinkService, requestSecurity)
	if err != nil {
		return fail(err)
	}
	channelCenter := channelstore.CenterHTTPHandler{Catalog: channelCatalog, Acquisition: channelAcquisitionHandler, History: channelHistoryHandler, Assets: channelAssetHandler}
	var callbackCrypto *wecom.CallbackCrypto
	var welcomeGrantStore *wecom.PostgreSQLWelcomeGrantStore
	if cfg.WeCom.CallbackEnabled {
		callbackCrypto, err = wecom.NewCallbackCrypto(cfg.WeCom.CallbackToken, cfg.WeCom.CallbackAESKey, cfg.WeCom.CorpID)
		if err != nil {
			return fail(err)
		}
		welcomeGrantCipher, cipherErr := wecom.NewWelcomeGrantCipher(cfg.WeCom.CallbackAESKey)
		if cipherErr != nil {
			return fail(cipherErr)
		}
		welcomeGrantStore = wecom.NewPostgreSQLWelcomeGrantStore(welcomeGrantCipher)
	}
	relationships := wecom.NewPostgreSQLFollowRelationshipStore()
	var channelAssetProvider effectport.ProviderAdapter
	var channelEntrantProvider effectport.ProviderAdapter
	var channelLinkProvider effectport.ProviderAdapter
	if cfg.WeCom.Enabled && cfg.WeCom.CallbackEnabled {
		channelAssetProvider = outbound.NewChannelAssetProvider(channelPublishedConfigAdapter{uow: uow, assets: channelAssetStore, users: accessRepository}, providerClient)
		channelEntrantProvider = outbound.NewChannelEntrantProvider(
			channelEntrantActionReaderAdapter{uow: uow, source: channelEntrantActions}, uow, welcomeGrantStore,
			channelCurrentContactAdapter{uow: uow, corpID: cfg.WeCom.CorpID, staff: accessRepository, relationships: relationships, identities: queries},
			channelProviderTagAdapter{uow: uow, tags: tagRepository}, providerClient,
		)
	}
	if cfg.WeCom.Enabled {
		channelLinkProvider = outbound.NewChannelLinkProvider(channelLinkMutationReaderAdapter{uow: uow, source: channelLinkStore}, providerClient)
	}
	privateProvider, err := outbound.NewPrivateMessageProvider(cfg.AIAssistant.DispatchEnabled, privateWriter, aiPrivateTargetResolver{uow: uow, identities: queries, access: accessRepository, relationships: relationships, corpID: cfg.WeCom.CorpID}, aiPrivatePayloadReader{content: aiRepository, images: mediaService, materials: mediaRepository}, providerClient)
	if err != nil {
		return fail(err)
	}
	var tagCatalogProvider externaleffects.ProviderAdapter
	if cfg.TagCatalog.Enabled {
		catalogReader, readerErr := outbound.NewWeComTagCatalogReader(providerClient)
		if readerErr != nil {
			return fail(readerErr)
		}
		catalogProvider, providerErr := outbound.NewTagCatalogProvider(catalogReader)
		if providerErr != nil {
			return fail(providerErr)
		}
		tagCatalogProvider = catalogProvider
	}
	messageProvider, providerErr := outbound.NewMessageProvider(outbound.MessageProviderConfig{Enabled: cfg.Effects.ProviderEnabled && cfg.WeCom.Enabled && cfg.AutomationOperations.ProviderEnabled(), CorpScope: "wecom-corp:" + cfg.WeCom.CorpID, Executions: outboundMessages, Identities: outboundIdentityAdapter{uow: uow, reader: queries}, Staff: segmentStaff, Content: automationService, Writer: providerClient})
	if providerErr != nil {
		return fail(providerErr)
	}
	providerRouter := outbound.NewProviderRouterWithGroupMessageAndChannels(tagCatalogProvider, groupOpsProvider, channelAssetProvider, channelEntrantProvider, channelLinkProvider).WithPrivateMessage(privateProvider).WithAutomationMessage(messageProvider)
	if err = effectsModule.SetProviderAdapter(composedProviderRouter{outbound: providerRouter, payment: paymentAdapter}); err != nil {
		return fail(err)
	}
	callbackReceipts := wecom.NewPostgreSQLCallbackReceiptStore()
	oauthStates := wecom.NewPostgreSQLOAuthStateStore()
	weComProcessor := wecom.InboxProcessor{
		Enabled: cfg.WeCom.CallbackEnabled, CorpID: cfg.WeCom.CorpID, Inbox: inboxService, UOW: uow,
		Lifecycle: wecom.ExternalContactLifecycle{
			Identity: oneID, Relationships: relationships, States: channelAcquisition, Entrants: channelAcquisition, Actions: channelEntrantActions,
			Directory: customerStore, Outbox: platformoutbox.NewPostgreSQL(),
		},
		Receipts: callbackReceipts, Audit: auditService,
	}
	customerSync := wecom.CustomerSyncService{Enabled: cfg.WeCom.CustomerSyncEnabled, CorpID: cfg.WeCom.CorpID, Provider: providerClient,
		Identity: oneID, Projection: customerStore, Timeline: customerStore, Store: customerProfileStore, Outbox: platformoutbox.NewPostgreSQL(),
		Enqueuer: customerSyncEnqueuer, Audit: auditService, UOW: uow}
	if err = customerSyncWorker.BindService(customerSync); err != nil && cfg.WeCom.CustomerSyncEnabled {
		return fail(err)
	}
	if cfg.HXCDashboard.Enabled {
		hxcSource, err = hxcprovider.Open(cfg.HXCDashboard.SourceDSN)
		if err != nil {
			return fail(err)
		}
	}
	hxcRepository := hxcstore.NewPostgreSQL(pool.Native())
	hxcDashboard := hxcapp.Service{Enabled: cfg.HXCDashboard.Enabled, Scope: cfg.HXCDashboard.UnionIDScope, SubjectKey: []byte(cfg.HXCDashboard.SubjectHMACKey), Source: hxcSource, Identity: queries, Store: hxcRepository, Enqueuer: hxcEnqueuer, Audit: auditService, UOW: uow}
	hxcDashboardWorker.Service = &hxcDashboard
	hxcHandler := hxchttp.Handler{Service: hxcDashboard, Store: hxcRepository, Auth: requestSecurity, Key: []byte(cfg.HXCDashboard.SubjectHMACKey)}
	syncHandler := wecom.CustomerSyncHTTPHandler{Service: customerSync, Auth: requestSecurity, CSRF: requestSecurity}
	weComHandler, err := wecom.NewHTTPHandler(wecom.HTTPHandlerOptions{
		Callback: wecom.CallbackHandler{Enabled: cfg.WeCom.CallbackEnabled, Crypto: callbackCrypto, StateDigester: callbackStateDigester, Inbox: inboxService, UOW: uow, WelcomeGrants: welcomeGrantStore},
		OAuth: wecom.OAuthService{Enabled: cfg.WeCom.Enabled, CorpID: cfg.WeCom.CorpID, StateStore: oauthStates, UOW: uow,
			Client: providerClient, AllowedPaths: allowedOAuthRedirects(), StateTTL: 10 * time.Minute},
		ContextTokens: wecom.ContextTokenService{CorpID: cfg.WeCom.CorpID, SigningKey: []byte(cfg.WeCom.ContextSigningKey),
			Relationships: relationships, UOW: uow, TTL: 5 * time.Minute},
		JSSDKSigner: providerClient, JSSDKOrigin: cfg.PublicOrigin,
		PrincipalResolver: sidebarPrincipalResolver{authentication: authentication, users: accessRepository, uow: uow, corpID: cfg.WeCom.CorpID},
		CustomerViewer:    sidebarCustomerViewer{queries: queries, uow: uow}, SessionIssuer: weComSessionIssuer{authentication: authentication},
		ExistingIdentity: existingWeComIdentityResolver{service: oneID, uow: uow, corpID: cfg.WeCom.CorpID}, CookieSecure: true,
	})
	if err != nil {
		return fail(err)
	}
	callbackAdminHandler, err := wecom.NewCallbackAdminHandler(wecom.CallbackAdminConfig{
		UnitOfWork: uow, Authenticator: requestSecurity, CSRF: requestSecurity,
		Receipts: callbackReceipts, Retrier: inboxService,
	})
	if err != nil {
		return fail(err)
	}
	entrantAdminHandler, err := channelstore.NewEntrantAdminHandler(channelstore.EntrantAdminConfig{
		UnitOfWork: uow, Authenticator: requestSecurity, CSRF: requestSecurity,
		Receipts: channelAcquisition, Audit: auditService,
	})
	if err != nil {
		return fail(err)
	}
	adminAPIs := http.NewServeMux()
	adminAPIs.Handle("/api/admin/oneid/", oneIDHandler.Routes())
	adminAPIs.Handle("/api/admin/wecom/", callbackAdminHandler.Routes())
	adminAPIs.Handle("/api/admin/channel-acquisition-entrant-receipts/", entrantAdminHandler.Routes())
	adminAPIs.Handle("/api/admin/customers", customerHandler.Routes())
	adminAPIs.Handle("/api/admin/customers/", customerHandler.Routes())
	adminAPIs.Handle("/api/admin/customer-sync-runs", syncHandler.Routes())
	adminAPIs.Handle("/api/admin/customer-sync-runs/", syncHandler.Routes())
	adminAPIs.Handle("/api/admin/hxc-dashboard/", hxcHandler.Routes())
	adminAPIs.Handle("/api/admin/orders", orderHandler)
	adminAPIs.Handle("/api/admin/orders/", orderHandler)
	adminAPIs.Handle("/api/admin/order-imports/", orderImportHandler)
	adminAPIs.Handle("/api/admin/refunds", paymentHandler)
	adminAPIs.Handle("/api/admin/exports", orderHandler)
	adminAPIs.Handle("/api/admin/exports/", orderHandler)
	adminAPIs.Handle("/api/admin/alipay/transactions", orderHandler)
	adminAPIs.Handle("/api/admin/wechat-pay/orders", orderHandler)
	adminAPIs.Handle("/api/admin/wechat-pay/orders/", paymentHandler)
	adminAPIs.Handle("/api/admin/wechat-shop/refunds/", paymentHandler)
	adminAPIs.Handle("/api/admin/wechat-pay/order-exports", orderHandler)
	adminAPIs.Handle("/api/admin/payments/", paymentHandler)
	adminAPIs.Handle("/api/v1/wechat-pay/", paymentHandler)
	adminAPIs.Handle("/api/public/wechat-pay/", paymentHandler)
	adminAPIs.Handle("/api/public/wechat-shop/", paymentHandler)
	adminAPIs.Handle("/api/v1/products", productBindings.Products)
	adminAPIs.Handle("/api/v1/products/", productBindings.Products)
	adminAPIs.Handle("/api/admin/wechat-pay/products", productBindings.Products)
	adminAPIs.Handle("/api/admin/wechat-pay/products/", productBindings.Products)
	adminAPIs.Handle("/api/admin/service-period-products", productBindings.Products)
	adminAPIs.Handle("/api/admin/service-period-products/", productBindings.Products)
	adminAPIs.Handle("/api/admin/coupons", couponBindings.Coupons)
	adminAPIs.Handle("/api/admin/coupons/", couponBindings.Coupons)
	adminAPIs.Handle("/api/admin/config/", configBindings.Config)
	adminAPIs.Handle("/api/admin/setup-wizard", configBindings.Config)
	adminAPIs.Handle("/api/admin/automation-agents", automationBindings.Agents)
	adminAPIs.Handle("/api/admin/automation-agents/", automationBindings.Agents)
	adminAPIs.Handle("/api/admin/automations", automationBindings.Runtime)
	adminAPIs.Handle("/api/admin/automations/", automationBindings.Runtime)
	adminAPIs.Handle("/api/admin/automation-runs", automationBindings.Runtime)
	adminAPIs.Handle("/api/admin/automation-runs/", automationBindings.Runtime)
	adminAPIs.Handle("/api/admin/channels", channelCenter)
	adminAPIs.Handle("/api/admin/channels/", channelCenter)
	adminAPIs.Handle("/api/admin/wecom-customer-acquisition-links", channelLinkHandler)
	adminAPIs.Handle("/api/admin/wecom-customer-acquisition-links/", channelLinkHandler)
	adminAPIs.Handle("/api/admin/ai-assistant/", aiHandler.Routes())
	mountSurveyAPIs(adminAPIs, surveyBindings.Survey)
	adminAPIs.Handle("/api/admin/operation-cycles/", operationBindings.API)
	adminAPIs.Handle("/api/operation-cycles/", operationBindings.API)
	readiness := platformruntime.ReadinessFunc(func(readinessContext context.Context) error {
		if checkErr := pool.Check(readinessContext); checkErr != nil {
			return checkErr
		}
		var complete bool
		checkErr := pool.Native().QueryRow(readinessContext, `SELECT NOT EXISTS (SELECT 1 FROM unnest(ARRAY['0001','0002','0003','0004','0005','0006','0007','0008','0009','0010','0011','0012','0013','0014','0015','0016','0017','0018','0019','0020','0021','0022','0023','0024','0025','0026','0027','0028','0029','0030','0031','0032','0033','0034','0035','0036','0037','0038','0039','0040','0041','0042','0043','0044','0045','0046','0047','0048','0049']) AS required(version) WHERE NOT EXISTS (SELECT 1 FROM platform_schema_migrations applied WHERE applied.version=required.version))`).Scan(&complete)
		if checkErr != nil || !complete {
			return errors.New("database schema is not ready")
		}
		if checkErr = effectsModule.Readiness(readinessContext, pool.Native()); checkErr != nil {
			return checkErr
		}
		if checkErr = mediaModule.Readiness(readinessContext, pool.Native()); checkErr != nil {
			return checkErr
		}
		if checkErr = aiModule.Readiness(readinessContext, pool.Native()); checkErr != nil {
			return checkErr
		}
		if checkErr = tagModule.Readiness(readinessContext, pool.Native()); checkErr != nil {
			return checkErr
		}
		if checkErr = channelModule.Readiness(readinessContext, pool.Native()); checkErr != nil {
			return checkErr
		}
		if checkErr = productModule.Readiness(readinessContext, pool.Native()); checkErr != nil {
			return checkErr
		}
		if checkErr = couponModule.Readiness(readinessContext, pool.Native()); checkErr != nil {
			return checkErr
		}
		if checkErr = configModule.Readiness(readinessContext, pool.Native()); checkErr != nil {
			return checkErr
		}
		if checkErr = configModule.Readiness(readinessContext, pool.Native()); checkErr != nil {
			return checkErr
		}
		if checkErr = automationModule.Readiness(readinessContext, pool.Native()); checkErr != nil {
			return checkErr
		}
		if checkErr = segmentModule.Readiness(readinessContext, pool.Native()); checkErr != nil {
			return checkErr
		}
		if checkErr = groupOpsModule.Readiness(readinessContext, pool.Native()); checkErr != nil {
			return checkErr
		}
		if checkErr = surveyModule.Readiness(readinessContext, pool.Native()); checkErr != nil {
			return checkErr
		}
		if checkErr = operationModule.Readiness(readinessContext, pool.Native()); checkErr != nil {
			return checkErr
		}
		if checkErr = adminOpsProjectionStore.Readiness(readinessContext); checkErr != nil {
			return checkErr
		}
		return nil
	})
	healthHandler, err := platformruntime.NewHandler(platformruntime.HandlerOptions{ReleaseSHA: cfg.ReleaseSHA, Readiness: readiness})
	if err != nil {
		return fail(err)
	}

	effectsUI := effectsModule.UIBinding("web/dist", func(writer http.ResponseWriter, request *http.Request, tokens, labs, admin string) error {
		return renderer.RenderExternalEffects(writer, webshell.AdminPageForRequest(request, "外部效果与 Push Center", "仅展示本地外部效果状态与对账事实。", "api.admin_cloud_orchestrator_workspace"), webshell.ExternalEffectsAssets{TokensCSS: tokens, LabsCSS: labs, AdminJS: admin})
	})
	hxcUI := hxchttp.NewUIHandler("web/dist", func(writer http.ResponseWriter, request *http.Request, assets hxchttp.PageAssets) error {
		return renderer.RenderHXC(writer, webshell.AdminPageForRequest(request, "漏斗 / 数据看板", "HXC 当前全量投影；OneID 仅作为次级质量指标。", "api.admin_hxc_dashboard_workspace"), webshell.HXCAssets{TokensCSS: assets.TokensCSS, LabsCSS: assets.LabsCSS, AdminJS: assets.AdminJS})
	})
	mediaUI := mediaModule.UIBinding("web/dist", func(writer http.ResponseWriter, request *http.Request, page, donorTemplate string, assets media.MediaAssets) error {
		endpoint := map[string]string{"images": "api.admin_image_library_workspace", "mpLib": "api.admin_miniprogram_library_workspace", "attach": "api.admin_attachment_library_workspace"}[page]
		return renderer.RenderMedia(writer, webshell.AdminPageForRequest(request, map[string]string{"images": "图片素材库", "mpLib": "小程序素材库", "attach": "附件素材库"}[page], "仅管理本地素材、私有 blob 与审计事实。", endpoint), page, donorTemplate, webshell.MediaAssets{TokensCSS: assets.TokensCSS, LabsCSS: assets.LabsCSS, AdminJS: assets.AdminJS})
	})
	tagUI := tagModule.UIBinding("web/dist", func(writer http.ResponseWriter, request *http.Request, donorTemplate string, assets tag.TagsAssets) error {
		return renderer.RenderTags(writer, webshell.AdminPageForRequest(request, "企微标签管理", "管理标签目录与本地同步意图。", "api.admin_wecom_tags_page"), donorTemplate, webshell.TagsAssets{TokensCSS: assets.TokensCSS, LabsCSS: assets.LabsCSS, AdminJS: assets.AdminJS})
	})
	productUI := productModule.UIBinding("web/dist", func(writer http.ResponseWriter, request *http.Request, page, donorTemplate string, assets productmodule.ProductAssets) error {
		titles := map[string]string{"products": "普通商品", "productForm": "普通商品", "spProducts": "周期商品", "spProductForm": "周期商品"}
		endpoints := map[string]string{"products": "api.admin_products_page", "productForm": "api.admin_product_form_page", "spProducts": "api.admin_service_period_products_page", "spProductForm": "api.admin_service_period_product_form_page"}
		return renderer.RenderProducts(writer, webshell.AdminPageForRequest(request, titles[page], "仅管理本地商品定义、生命周期与受控配置。", endpoints[page]), page, donorTemplate, webshell.ProductAssets{TokensCSS: assets.TokensCSS, LabsCSS: assets.LabsCSS, AdminJS: assets.AdminJS})
	})
	orderUI := orderui.NewUIBinding("web/dist", func(writer http.ResponseWriter, request *http.Request, page, donorTemplate string, assets orderui.PageAssets) error {
		title := map[string]string{"orders": "交易管理", "orderDetail": "订单详情"}[page]
		return renderer.RenderOrders(writer, webshell.AdminPageForRequest(request, title, "历史订单默认只读；未验证身份不归属 OneID。", "api.admin_orders_page"), page, donorTemplate, webshell.OrderAssets{TokensCSS: assets.TokensCSS, LabsCSS: assets.LabsCSS, AdminJS: assets.AdminJS})
	})
	couponUI := couponModule.UIBinding("web/dist", func(writer http.ResponseWriter, request *http.Request, page, donorTemplate string, assets coupon.Assets) error {
		titles := map[string]string{"coupons": "优惠券", "couponForm": "优惠券"}
		endpoints := map[string]string{"coupons": "api.admin_coupons_page", "couponForm": "api.admin_coupon_form_page"}
		return renderer.RenderCoupons(writer, webshell.AdminPageForRequest(request, titles[page], "仅管理本地优惠券规则，不含领取、核销、客户持券或订单。", endpoints[page]), page, donorTemplate, webshell.CouponAssets{TokensCSS: assets.TokensCSS, LabsCSS: assets.LabsCSS, AdminJS: assets.AdminJS})
	})
	groupOpsUI := groupOpsModule.UIBinding("web/dist", func(writer http.ResponseWriter, request *http.Request, page, donorTemplate string, assets groupops.GroupOpsAssets) error {
		endpoint := "api.admin_group_ops_ui"
		if page == "groupopsDetail" {
			endpoint = "api.admin_group_ops_plan_detail"
		}
		return renderer.RenderGroupOps(writer, webshell.AdminPageForRequest(request, "群运营计划", "管理本地群计划、节点、素材快照与执行回执。", endpoint), page, donorTemplate, webshell.GroupOpsAssets{TokensCSS: assets.TokensCSS, LabsCSS: assets.LabsCSS, AdminJS: assets.AdminJS})
	})
	automationUI := automationModule.UIBinding("web/dist", func(writer http.ResponseWriter, request *http.Request, page, donorTemplate string, assets automation.AgentAssets, bootstrap automation.AgentPageBootstrap) error {
		return renderer.RenderAutomation(writer, webshell.AdminPageForRequest(request, "自动化话术", "管理本地 Agent 与固定话术配置。", "api.admin_automation_agents"), page, donorTemplate, webshell.AutomationAssets{TokensCSS: assets.TokensCSS, LabsCSS: assets.LabsCSS, AdminJS: assets.AdminJS}, bootstrap.CreateCode)
	})
	surveyUI := surveyModule.UIBinding("web/dist", func(writer http.ResponseWriter, request *http.Request, page, donorTemplate string, assets surveymodule.UIAssets) error {
		titles := map[string]string{"questionnaires": "问卷管理", "questionnaireDetail": "问卷编辑", "questionnaireOps": "问卷运营"}
		return renderer.RenderSurvey(writer, webshell.AdminPageForRequest(request, titles[page], "管理问卷定义、版本、答卷及只读外部效果回执。", "api.admin_questionnaires"), page, donorTemplate, webshell.SurveyAssets{TokensCSS: assets.TokensCSS, LabsCSS: assets.LabsCSS, AdminJS: assets.AdminJS, EditorJS: assets.EditorJS, EditorCSS: assets.EditorCSS})
	})
	surveyPublicUI := surveyModule.PublicUIBinding("web/dist")
	operationUI := operationModule.UIBinding("web/dist", func(writer http.ResponseWriter, request *http.Request, page, donorTemplate string, assets operationcycle.UIAssets) error {
		return renderer.RenderOperationCycles(writer, webshell.AdminPageForRequest(request, "运营闭环", "运营周期、执行事实与复盘记录。", "api.admin_operation_cycles_page"), page, donorTemplate, webshell.OperationCycleAssets{TokensCSS: assets.TokensCSS, LabsCSS: assets.LabsCSS, HostJS: assets.HostJS})
	})
	configUI := configModule.UIBinding("web/dist", func(writer http.ResponseWriter, request *http.Request, page, donorTemplate string, assets configmodule.UIAssets) error {
		title := map[string]string{"config": "配置", "configDetail": "配置", "apidocs": "API 文档"}[page]
		endpoint := map[string]string{"config": "api.admin_config", "configDetail": "api.admin_config", "apidocs": "api.admin_api_docs"}[page]
		return renderer.RenderConfig(writer, webshell.AdminPageForRequest(request, title, "", endpoint), page, donorTemplate, webshell.ConfigAssets{TokensCSS: assets.TokensCSS, LabsCSS: assets.LabsCSS, AdminJS: assets.AdminJS})
	})
	channelUI := channelModule.UIBinding("web/dist", func(writer http.ResponseWriter, request *http.Request, page, resourceID, donorTemplate string, assets channelstore.UIAssets) error {
		title := map[string]string{"channels": "渠道码中心", "channelForm": "渠道配置"}[page]
		endpoint := map[string]string{"channels": "api.admin_channels_page", "channelForm": "api.admin_channel_new_page"}[page]
		return renderer.RenderChannels(writer, webshell.AdminPageForRequest(request, title, "管理渠道定义、客服分配、资产状态与安全历史归因。", endpoint), page, resourceID, donorTemplate, webshell.ChannelAssets{TokensCSS: assets.TokensCSS, LabsCSS: assets.LabsCSS, AdminJS: assets.AdminJS})
	})
	aiUI := aiModule.UIBinding("web/dist", func(writer http.ResponseWriter, request *http.Request, page, donorTemplate string, assets aiassistant.Assets) error {
		return renderer.RenderAIAssistant(writer, webshell.AdminPageForRequest(request, "AI 助手", "AI 计划审阅与可对账执行结果。", "api.admin_ai_assistant"), page, donorTemplate, webshell.AIAssistantAssets{TokensCSS: assets.TokensCSS, LabsCSS: assets.LabsCSS, GroupCSS: assets.GroupCSS, MaterialCSS: assets.MaterialCSS, ComposerCSS: assets.ComposerCSS, ReadonlyCSS: assets.ReadonlyCSS, HostJS: assets.HostJS})
	})
	handler, err := routeApplicationWithProductsCouponsGroupOpsAutomationAndCycles(healthHandler, accessHandler.Routes(), adminAPIs, effectsBindings.Effects, effectsBindings.PushCenter, effectsUI, mediaBindings.Media, mediaUI, tagBindings.Tags, tagUI, productBindings.Products, productUI, couponBindings.Coupons, couponUI, channelCenter, groupOpsBindings.GroupOps, groupOpsUI, automationBindings.Agents, automationUI, operationUI, configBindings.Config, configUI, weComHandler, shellHandler, authentication, cfg.PublicOrigin)
	if err != nil {
		return fail(err)
	}
	handler, err = mountSegmentAPI(handler, segmentBindings.Audience)
	if err != nil {
		return fail(err)
	}
	handler, err = mountAutomationRuntimeAPI(handler, automationBindings.Runtime)
	if err != nil {
		return fail(err)
	}
	handler, err = mountSegmentWebhook(handler, segmentWebhookHandler)
	if err != nil {
		return fail(err)
	}
	handler = mountAIAssistant(handler, aiHandler.Routes(), aiUI, authentication, cfg.AIAssistant.UIEnabled, cfg.PublicOrigin)
	handler = securityHeaders(mountChannelUI(mountHXCUI(mountOrderUI(mountSurveyUI(handler, surveyUI, surveyPublicUI, authentication), orderUI, authentication), hxcUI, authentication), channelUI, authentication))
	// These are local observations only: they make the release and diagnostics
	// projections truthful and readable after startup, without claiming deploy,
	// cutover, provider execution, or runtime-secret application.
	if err = releaseObservation.Record(ctx, releaseport.ReleaseObservation{ReleaseSHA: cfg.ReleaseSHA, Status: "observed"}); err != nil {
		return fail(err)
	}
	if _, err = diagnostics.Record(ctx, adminopsport.DiagnosticSnapshot{Key: "aicrm.composition", Status: "ok"}); err != nil {
		return fail(err)
	}
	return &composedApplication{pool: pool, handler: handler, management: management, weComProcessor: weComProcessor, effectsRuntime: effectsRuntime, customerSync: customerSync, hxcDashboard: hxcDashboard, hxcSource: hxcSource, adminOps: adminOpsProjection, release: releaseObservation, diagnostics: diagnostics}, nil
}

func mountSurveyAPIs(mux *http.ServeMux, survey http.Handler) {
	mux.Handle("/api/admin/questionnaires", survey)
	mux.Handle("/api/admin/questionnaires/", survey)
	mux.Handle("/api/admin/survey-history/", survey)
	mux.Handle("/api/public/questionnaires/", survey)
	mux.Handle("/api/public/survey-submission-results/query", survey)
	mux.Handle("/api/h5/surveys/oauth/", survey)
	mux.Handle("/api/h5/surveys/session", survey)
	mux.Handle("/q/", survey)
	mux.Handle("/api/sidebar/v2/questionnaires", survey)
	mux.Handle("/api/v1/customers/", survey)
	// The frozen operations workspace reads its history projection from this
	// legacy page-shaped path. Keep it inside the authenticated admin mux so the
	// response is JSON from Survey instead of the outer mux's plain-text 404.
	mux.Handle("/admin/questionnaires/", survey)
}

func mountHXCUI(next, dashboardUI http.Handler, authentication accessAuthentication) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/admin/hxc-dashboard" || strings.HasPrefix(request.URL.Path, "/hxc-dashboard-assets/") {
			requireAdminSession(authentication, dashboardUI).ServeHTTP(writer, request)
			return
		}
		next.ServeHTTP(writer, request)
	})
}

func mountAIAssistant(next, api, ui http.Handler, authentication accessAuthentication, uiEnabled bool, publicOrigin string) http.Handler {
	api = rejectCrossSiteUnsafeRequests(api, canonicalOrigin(publicOrigin))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/admin/ai-assistant/") || r.URL.Path == "/api/integrations/ai-assistant/review-plans" {
			api.ServeHTTP(w, r)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/ai-assistant-assets/") || r.URL.Path == "/admin/ai.html" || r.URL.Path == "/admin/aiDetail.html" || r.URL.Path == "/admin/cloud-orchestrator/plans" || strings.HasPrefix(r.URL.Path, "/admin/cloud-orchestrator/plans/") {
			if !uiEnabled {
				http.NotFound(w, r)
				return
			}
			requireAdminSession(authentication, ui).ServeHTTP(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func mountOrderUI(next, adminUI http.Handler, authentication accessAuthentication) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/admin/orders", "/admin/orders.html", "/admin/orderDetail.html":
			requireAdminSession(authentication, adminUI).ServeHTTP(w, r)
		default:
			if strings.HasPrefix(r.URL.Path, "/order-assets/") {
				requireAdminSession(authentication, adminUI).ServeHTTP(w, r)
				return
			}
			next.ServeHTTP(w, r)
		}
	})
}

func mountSurveyUI(next, adminUI, publicUI http.Handler, authentication accessAuthentication) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/admin/questionnaires" || r.URL.Path == "/admin/questionnaires.html" || r.URL.Path == "/admin/questionnaireDetail.html" || r.URL.Path == "/admin/questionnaireOps.html":
			requireAdminSession(authentication, adminUI).ServeHTTP(w, r)
		case strings.HasPrefix(r.URL.Path, "/h5/") || strings.HasPrefix(r.URL.Path, "/survey-assets/"):
			publicUI.ServeHTTP(w, r)
		default:
			next.ServeHTTP(w, r)
		}
	})
}

func mountChannelUI(next, adminUI http.Handler, authentication accessAuthentication) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimSuffix(r.URL.Path, "/")
		isCanonicalEdit := strings.HasPrefix(path, "/admin/channels/") && strings.HasSuffix(path, "/edit")
		switch {
		case path == "/admin/channels", path == "/admin/channels.html", path == "/admin/channels/new", path == "/admin/channelForm.html", isCanonicalEdit:
			requireAdminSession(authentication, adminUI).ServeHTTP(w, r)
		default:
			next.ServeHTTP(w, r)
		}
	})
}

func (application *composedApplication) Close() {
	if application != nil && application.hxcSource != nil {
		_ = application.hxcSource.Close()
	}
	if application != nil && application.pool != nil {
		application.pool.Close()
	}
}

func (application *composedApplication) bootstrap(ctx context.Context, config platformconfig.Bootstrap) error {
	if !config.Enabled {
		return nil
	}
	_, _, err := application.management.Bootstrap(ctx, accessapp.BootstrapInput{
		Username: config.Username, Password: config.Password, DisplayName: config.DisplayName,
	})
	return err
}

func allowedOAuthRedirects() map[string]struct{} {
	paths := map[string]struct{}{webshell.SidebarPagePath: {}}
	for _, route := range webshell.ADMIN_ROUTE_REGISTRY {
		if strings.HasPrefix(route.Path, webshell.AdminRootPath) {
			paths[route.Path] = struct{}{}
		}
	}
	return paths
}

func routeApplication(health, access, identity, weCom, shell http.Handler, authentication accessAuthentication, publicOrigin string) (http.Handler, error) {
	return routeApplicationWithEffects(health, access, identity, http.NotFoundHandler(), http.NotFoundHandler(), http.NotFoundHandler(), weCom, shell, authentication, publicOrigin)
}

func routeApplicationWithEffects(health, access, identity, effects, pushCenter, effectsUI, weCom, shell http.Handler, authentication accessAuthentication, publicOrigin string) (http.Handler, error) {
	return routeApplicationWithMedia(health, access, identity, effects, pushCenter, effectsUI, http.NotFoundHandler(), http.NotFoundHandler(), weCom, shell, authentication, publicOrigin)
}

func routeApplicationWithMedia(health, access, identity, effects, pushCenter, effectsUI, mediaHandler, mediaUI, weCom, shell http.Handler, authentication accessAuthentication, publicOrigin string) (http.Handler, error) {
	return routeApplicationWithMediaTags(health, access, identity, effects, pushCenter, effectsUI, mediaHandler, mediaUI, http.NotFoundHandler(), http.NotFoundHandler(), weCom, shell, authentication, publicOrigin)
}

func routeApplicationWithMediaTags(health, access, identity, effects, pushCenter, effectsUI, mediaHandler, mediaUI, tagHandler, tagUI, weCom, shell http.Handler, authentication accessAuthentication, publicOrigin string) (http.Handler, error) {
	return routeApplicationWithProducts(health, access, identity, effects, pushCenter, effectsUI, mediaHandler, mediaUI, tagHandler, tagUI, http.NotFoundHandler(), http.NotFoundHandler(), http.NotFoundHandler(), weCom, shell, authentication, publicOrigin)
}

func routeApplicationWithProducts(health, access, identity, effects, pushCenter, effectsUI, mediaHandler, mediaUI, tagHandler, tagUI, productHandler, productUI, channelHandler, weCom, shell http.Handler, authentication accessAuthentication, publicOrigin string) (http.Handler, error) {
	return routeApplicationWithProductsCoupons(health, access, identity, effects, pushCenter, effectsUI, mediaHandler, mediaUI, tagHandler, tagUI, productHandler, productUI, http.NotFoundHandler(), http.NotFoundHandler(), channelHandler, weCom, shell, authentication, publicOrigin)
}

func routeApplicationWithProductsCoupons(health, access, identity, effects, pushCenter, effectsUI, mediaHandler, mediaUI, tagHandler, tagUI, productHandler, productUI, couponHandler, couponUI, channelHandler, weCom, shell http.Handler, authentication accessAuthentication, publicOrigin string) (http.Handler, error) {
	return routeApplicationWithProductsCouponsGroupOps(health, access, identity, effects, pushCenter, effectsUI, mediaHandler, mediaUI, tagHandler, tagUI, productHandler, productUI, couponHandler, couponUI, channelHandler, http.NotFoundHandler(), http.NotFoundHandler(), http.NotFoundHandler(), http.NotFoundHandler(), weCom, shell, authentication, publicOrigin)
}

// routeApplicationWithGroupOps keeps the pre-Product/Coupon composition helper
// available to tests while the real Composition Root mounts every owned module
// through the combined route below.
func routeApplicationWithGroupOps(health, access, identity, effects, pushCenter, effectsUI, mediaHandler, mediaUI, tagHandler, tagUI, groupOpsHandler, groupOpsUI, weCom, shell http.Handler, authentication accessAuthentication, publicOrigin string) (http.Handler, error) {
	return routeApplicationWithProductsCouponsGroupOps(health, access, identity, effects, pushCenter, effectsUI, mediaHandler, mediaUI, tagHandler, tagUI, http.NotFoundHandler(), http.NotFoundHandler(), http.NotFoundHandler(), http.NotFoundHandler(), http.NotFoundHandler(), groupOpsHandler, groupOpsUI, http.NotFoundHandler(), http.NotFoundHandler(), weCom, shell, authentication, publicOrigin)
}

func routeApplicationWithProductsCouponsGroupOps(health, access, identity, effects, pushCenter, effectsUI, mediaHandler, mediaUI, tagHandler, tagUI, productHandler, productUI, couponHandler, couponUI, channelHandler, groupOpsHandler, groupOpsUI, automationHandler, automationUI, weCom, shell http.Handler, authentication accessAuthentication, publicOrigin string) (http.Handler, error) {
	return routeApplicationWithProductsCouponsGroupOpsAutomationAndCycles(health, access, identity, effects, pushCenter, effectsUI, mediaHandler, mediaUI, tagHandler, tagUI, productHandler, productUI, couponHandler, couponUI, channelHandler, groupOpsHandler, groupOpsUI, automationHandler, automationUI, http.NotFoundHandler(), http.NotFoundHandler(), http.NotFoundHandler(), weCom, shell, authentication, publicOrigin)
}

func routeApplicationWithAll(health, access, identity, effects, pushCenter, effectsUI, mediaHandler, mediaUI, tagHandler, tagUI, operationUI, weCom, shell http.Handler, authentication accessAuthentication, publicOrigin string) (http.Handler, error) {
	return routeApplicationWithProductsCouponsGroupOpsAutomationAndCycles(health, access, identity, effects, pushCenter, effectsUI, mediaHandler, mediaUI, tagHandler, tagUI, http.NotFoundHandler(), http.NotFoundHandler(), http.NotFoundHandler(), http.NotFoundHandler(), http.NotFoundHandler(), http.NotFoundHandler(), http.NotFoundHandler(), http.NotFoundHandler(), http.NotFoundHandler(), operationUI, http.NotFoundHandler(), http.NotFoundHandler(), weCom, shell, authentication, publicOrigin)
}

func routeApplicationWithProductsCouponsAndCycles(health, access, identity, effects, pushCenter, effectsUI, mediaHandler, mediaUI, tagHandler, tagUI, productHandler, productUI, couponHandler, couponUI, channelHandler, operationUI, weCom, shell http.Handler, authentication accessAuthentication, publicOrigin string) (http.Handler, error) {
	return routeApplicationWithProductsCouponsGroupOpsAutomationAndCycles(health, access, identity, effects, pushCenter, effectsUI, mediaHandler, mediaUI, tagHandler, tagUI, productHandler, productUI, couponHandler, couponUI, channelHandler, http.NotFoundHandler(), http.NotFoundHandler(), http.NotFoundHandler(), http.NotFoundHandler(), operationUI, http.NotFoundHandler(), http.NotFoundHandler(), weCom, shell, authentication, publicOrigin)
}

func routeApplicationWithProductsCouponsGroupOpsAndCycles(health, access, identity, effects, pushCenter, effectsUI, mediaHandler, mediaUI, tagHandler, tagUI, productHandler, productUI, couponHandler, couponUI, channelHandler, groupOpsHandler, groupOpsUI, operationUI, weCom, shell http.Handler, authentication accessAuthentication, publicOrigin string) (http.Handler, error) {
	return routeApplicationWithProductsCouponsGroupOpsAutomationAndCycles(health, access, identity, effects, pushCenter, effectsUI, mediaHandler, mediaUI, tagHandler, tagUI, productHandler, productUI, couponHandler, couponUI, channelHandler, groupOpsHandler, groupOpsUI, http.NotFoundHandler(), http.NotFoundHandler(), operationUI, http.NotFoundHandler(), http.NotFoundHandler(), weCom, shell, authentication, publicOrigin)
}

func routeApplicationWithProductsCouponsGroupOpsAutomationAndCycles(health, access, identity, effects, pushCenter, effectsUI, mediaHandler, mediaUI, tagHandler, tagUI, productHandler, productUI, couponHandler, couponUI, channelHandler, groupOpsHandler, groupOpsUI, automationHandler, automationUI, operationUI, configHandler, configUI, weCom, shell http.Handler, authentication accessAuthentication, publicOrigin string) (http.Handler, error) {
	if health == nil || access == nil || identity == nil || effects == nil || pushCenter == nil || effectsUI == nil || mediaHandler == nil || mediaUI == nil || tagHandler == nil || tagUI == nil || productHandler == nil || productUI == nil || couponHandler == nil || couponUI == nil || channelHandler == nil || groupOpsHandler == nil || groupOpsUI == nil || automationHandler == nil || automationUI == nil || operationUI == nil || configHandler == nil || configUI == nil || weCom == nil || shell == nil || authentication == nil || canonicalOrigin(publicOrigin) == "" {
		return nil, errors.New("application HTTP dependencies are required")
	}
	mux := http.NewServeMux()
	mux.Handle("/healthz", health)
	mux.Handle("/readyz", health)
	mux.Handle("/login", access)
	mux.Handle("/logout", access)
	mux.Handle("/api/admin/access/", access)
	// Frozen PR09 AdminOps requests this exact compatibility URL. Access remains
	// the route owner; Config never receives or reimplements admin credentials.
	mux.Handle("/api/admin/admin-access", access)
	mux.Handle("/api/admin/oneid/", identity)
	mux.Handle("/api/admin/wecom/", identity)
	mux.Handle("/api/admin/channel-acquisition-entrant-receipts/", identity)
	mux.Handle("/api/admin/customers", identity)
	mux.Handle("/api/admin/customers/", identity)
	mux.Handle("/api/admin/customer-sync-runs", identity)
	mux.Handle("/api/admin/customer-sync-runs/", identity)
	mux.Handle("/api/admin/hxc-dashboard/", identity)
	mux.Handle("/api/admin/questionnaires", identity)
	mux.Handle("/api/admin/questionnaires/", identity)
	mux.Handle("/api/admin/survey-history/", identity)
	mux.Handle("/api/public/questionnaires/", identity)
	mux.Handle("/api/public/survey-submission-results/query", identity)
	mux.Handle("/api/h5/surveys/oauth/", identity)
	mux.Handle("/api/h5/surveys/session", identity)
	mux.Handle("/q/", identity)
	mux.Handle("/api/sidebar/v2/questionnaires", identity)
	mux.Handle("/api/v1/customers/", identity)
	mux.Handle("/admin/questionnaires/", identity)
	mux.Handle("/api/admin/orders", identity)
	mux.Handle("/api/admin/orders/", identity)
	mux.Handle("/api/admin/order-imports/", identity)
	mux.Handle("/api/admin/refunds", identity)
	mux.Handle("/api/admin/exports", identity)
	mux.Handle("/api/admin/exports/", identity)
	mux.Handle("/api/admin/alipay/transactions", identity)
	mux.Handle("/api/admin/wechat-pay/orders", identity)
	mux.Handle("/api/admin/payments/", identity)
	mux.Handle("/api/v1/wechat-pay/", identity)
	mux.Handle("/api/public/wechat-pay/", identity)
	mux.Handle("/api/public/wechat-shop/", identity)
	mux.Handle("/api/admin/wechat-pay/orders/", identity)
	mux.Handle("/api/admin/wechat-shop/refunds/", identity)
	mux.Handle("/api/admin/wechat-pay/order-exports", identity)
	mux.Handle("/api/admin/operation-cycles/", identity)
	mux.Handle("/api/operation-cycles/", identity)
	mux.Handle("/api/admin/external-effects", effects)
	mux.Handle("/api/admin/external-effects/", effects)
	mux.Handle("/api/admin/push-center/", pushCenter)
	mux.Handle("/api/admin/image-library", mediaHandler)
	mux.Handle("/api/admin/image-library/", mediaHandler)
	mux.Handle("/api/admin/attachment-library", mediaHandler)
	mux.Handle("/api/admin/attachment-library/", mediaHandler)
	mux.Handle("/api/admin/miniprogram-library", mediaHandler)
	mux.Handle("/api/admin/miniprogram-library/", mediaHandler)
	mux.Handle("/api/admin/group-invite-library", mediaHandler)
	mux.Handle("/api/admin/group-invite-library/", mediaHandler)
	mux.Handle("/api/admin/wecom/tags", tagHandler)
	mux.Handle("/api/admin/wecom/tags/", tagHandler)
	mux.Handle("/api/admin/wecom/tag-groups", tagHandler)
	mux.Handle("/api/admin/wecom/tag-groups/", tagHandler)
	// Product API paths are registered before the generic admin compatibility
	// handler. Product owns only local definitions/lifecycle/configuration;
	// member-grid and provider paths remain absent/fail-closed.
	mux.Handle("/api/v1/products", productHandler)
	mux.Handle("/api/v1/products/", productHandler)
	mux.Handle("/api/admin/wechat-pay/products", productHandler)
	mux.Handle("/api/admin/wechat-pay/products/", productHandler)
	mux.Handle("/api/admin/service-period-products", productHandler)
	mux.Handle("/api/admin/service-period-products/", productHandler)
	mux.Handle("/api/admin/coupons", couponHandler)
	mux.Handle("/api/admin/coupons/", couponHandler)
	mux.Handle("/api/admin/config/", configHandler)
	mux.Handle("/api/admin/setup-wizard", configHandler)
	mux.Handle("/api/admin/channels", channelHandler)
	mux.Handle("/api/admin/channels/", channelHandler)
	mux.Handle("/api/admin/wecom-customer-acquisition-links", identity)
	mux.Handle("/api/admin/wecom-customer-acquisition-links/", identity)
	mux.Handle("/api/admin/automation-conversion/group-ops/", groupOpsHandler)
	mux.Handle("/api/admin/automation-agents", automationHandler)
	mux.Handle("/api/admin/automation-agents/", automationHandler)
	mux.Handle("/api/admin/common/operation-members", groupOpsHandler)
	mux.Handle("/api/admin/common/operation-members/", groupOpsHandler)
	mux.Handle("/api/automation/group-ops/", groupOpsHandler)
	mux.Handle("/assets/", requireAdminSession(authentication, effectsUI))
	mux.Handle("/media-assets/", requireAdminSession(authentication, mediaUI))
	mux.Handle("/product-assets/", requireAdminSession(authentication, productUI))
	mux.Handle("/coupon-assets/", requireAdminSession(authentication, couponUI))
	mux.Handle("/groupops-assets/", requireAdminSession(authentication, groupOpsUI))
	mux.Handle("/automation-assets/", requireAdminSession(authentication, automationUI))
	mux.Handle("/config-assets/", requireAdminSession(authentication, configUI))
	mux.Handle("/admin/wecom-tags", requireAdminSession(authentication, tagUI))
	mux.Handle("/admin/operation-cycles", requireAdminSession(authentication, operationUI))
	mux.Handle("/admin/operation-cycles/", requireAdminSession(authentication, operationUI))
	// The staged Tags donor document is a private template carrier. Only the
	// canonical PR10-mounted route above is public; neither its private staging
	// name nor the donor document name may fall through to a generic 200 shell.
	mux.Handle("/admin/tags.html", http.NotFoundHandler())
	mux.Handle("/admin/wecom-tags.html", http.NotFoundHandler())
	mux.Handle("/admin/cycles.html", http.NotFoundHandler())
	mux.Handle("/admin/cyclesDetail.html", http.NotFoundHandler())
	mux.Handle("/admin/external-effects", requireAdminSession(authentication, effectsUI))
	mux.Handle("/admin/campaigns.html", requireAdminSession(authentication, effectsUI))
	mux.Handle("/admin/image-library", requireAdminSession(authentication, mediaUI))
	mux.Handle("/admin/miniprogram-library", requireAdminSession(authentication, mediaUI))
	mux.Handle("/admin/attachment-library", requireAdminSession(authentication, mediaUI))
	// PR04 canonical/nested Product aliases all mount the donor template#tpl
	// fragment in admin_base. Exact spProductData paths are denied before the
	// generic admin shell so the excluded member-grid page cannot boot.
	for _, path := range []string{
		"/admin/wechat-pay/products", "/admin/wechat-pay/products/",
		"/admin/wechat-pay/products.html", "/admin/products.html",
		"/admin/wechat-pay/productForm.html", "/admin/productForm.html",
		"/admin/wechat-pay/spProducts.html", "/admin/spProducts.html",
		"/admin/wechat-pay/spProductForm.html", "/admin/spProductForm.html",
		"/admin/service-period-products", "/admin/service-period-products/",
		"/admin/wechat-pay/products/new", "/admin/service-period-products/new",
	} {
		mux.Handle(path, requireAdminSession(authentication, productUI))
	}
	// The coupon donor documents are private template carriers. The two exact
	// v2 routes below are the only public mounts; claim/redeem and public-link
	// routes remain absent rather than receiving a shell placeholder.
	for _, path := range []string{"/admin/coupons", "/admin/coupons.html", "/admin/couponForm.html"} {
		mux.Handle(path, requireAdminSession(authentication, couponUI))
	}
	mux.Handle("/admin/couponData.html", requireAdminSession(authentication, http.NotFoundHandler()))
	for _, path := range []string{
		"/admin/spProductData.html", "/admin/wechat-pay/spProductData.html",
		"/admin/wechat-pay/products/spProductData.html", "/admin/service-period-products/spProductData.html",
	} {
		mux.Handle(path, requireAdminSession(authentication, http.NotFoundHandler()))
	}
	mux.Handle("/admin/automation-conversion/group-ops/ui", requireAdminSession(authentication, groupOpsUI))
	mux.Handle("/admin/automation-conversion/group-ops/groups/ui", requireAdminSession(authentication, groupOpsUI))
	mux.Handle("/admin/automation-conversion/group-ops/plans/", requireAdminSession(authentication, groupOpsUI))
	mux.Handle("/admin/groupops.html", requireAdminSession(authentication, groupOpsUI))
	mux.Handle("/admin/groupopsDetail.html", requireAdminSession(authentication, groupOpsUI))
	for _, path := range []string{"/admin/automation-agents", "/admin/automation-agents/", "/admin/agents.html", "/admin/agentEdit.html"} {
		mux.Handle(path, requireAdminSession(authentication, automationUI))
	}
	for _, path := range []string{"/admin/config", "/admin/config/", "/admin/config.html", "/admin/configDetail.html", "/admin/api-docs", "/admin/apidocs.html"} {
		mux.Handle(path, requireAdminSession(authentication, configUI))
	}
	mux.Handle("/wecom/external-contact/callback", weCom)
	mux.Handle("/api/wecom/events", weCom)
	mux.Handle("/auth/wecom/start", weCom)
	mux.Handle("/auth/wecom/callback", weCom)
	mux.Handle("/api/sidebar/", weCom)
	mux.Handle("/static/", shell)
	mux.Handle(webshell.SidebarPagePath, shell)
	mux.Handle("/admin", requireAdminSession(authentication, shell))
	mux.Handle("/admin/", requireAdminSession(authentication, shell))
	mux.HandleFunc("/", func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/" {
			http.NotFound(writer, request)
			return
		}
		http.Redirect(writer, request, "/admin", http.StatusSeeOther)
	})
	return securityHeaders(rejectCrossSiteUnsafeRequests(mux, canonicalOrigin(publicOrigin))), nil
}

func rejectCrossSiteUnsafeRequests(next http.Handler, publicOrigin string) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if isUnsafeMethod(request.Method) && !usesIndependentLoginCSRF(request) {
			origin := request.Header.Get("Origin")
			blocked := false
			if origin != "" {
				// Origin is the authoritative browser signal. Fetch Metadata is
				// only a fallback because extensions and restored tabs can report
				// an inconsistent Sec-Fetch-Site for an otherwise same-origin form.
				blocked = canonicalOrigin(origin) != publicOrigin
			} else {
				blocked = strings.EqualFold(request.Header.Get("Sec-Fetch-Site"), "cross-site")
			}
			if blocked {
				writer.Header().Set("Content-Type", "application/json")
				writer.WriteHeader(http.StatusForbidden)
				_, _ = writer.Write([]byte(`{"ok":false,"error":"cross_site_request"}`))
				return
			}
		}
		next.ServeHTTP(writer, request)
	})
}

func usesIndependentLoginCSRF(request *http.Request) bool {
	return request.Method == http.MethodPost && request.URL.Path == "/login"
}

func isUnsafeMethod(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

func canonicalOrigin(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return ""
	}
	if parsed.Scheme != "https" {
		return ""
	}
	return parsed.Scheme + "://" + parsed.Host
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		writer.Header().Set("X-Content-Type-Options", "nosniff")
		writer.Header().Set("Referrer-Policy", "no-referrer")
		writer.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		styleSource := "'self'"
		mediaPage := request.URL.Path == "/admin/image-library" || request.URL.Path == "/admin/miniprogram-library" || request.URL.Path == "/admin/attachment-library"
		tagsPage := request.URL.Path == "/admin/wecom-tags"
		productPage := isProductShellPath(request.URL.Path)
		orderPage := request.URL.Path == "/admin/orders" || request.URL.Path == "/admin/orders.html" || request.URL.Path == "/admin/orderDetail.html"
		couponPage := request.URL.Path == "/admin/coupons" || request.URL.Path == "/admin/coupons.html" || request.URL.Path == "/admin/couponForm.html"
		groupOpsPage := request.URL.Path == "/admin/automation-conversion/group-ops/ui" || request.URL.Path == "/admin/automation-conversion/group-ops/groups/ui" || request.URL.Path == "/admin/groupops.html" || request.URL.Path == "/admin/groupopsDetail.html" || strings.HasPrefix(request.URL.Path, "/admin/automation-conversion/group-ops/plans/")
		automationPage := request.URL.Path == "/admin/automation-agents" || strings.HasPrefix(request.URL.Path, "/admin/automation-agents/") || request.URL.Path == "/admin/agents.html" || request.URL.Path == "/admin/agentEdit.html"
		surveyPage := request.URL.Path == "/admin/questionnaires" || request.URL.Path == "/admin/questionnaires.html" || request.URL.Path == "/admin/questionnaireDetail.html" || request.URL.Path == "/admin/questionnaireOps.html" || strings.HasPrefix(request.URL.Path, "/h5/")
		operationCyclesPage := request.URL.Path == "/admin/operation-cycles" || strings.HasPrefix(request.URL.Path, "/admin/operation-cycles/")
		configPage := request.URL.Path == "/admin/config" || request.URL.Path == "/admin/config.html" || request.URL.Path == "/admin/configDetail.html" || request.URL.Path == "/admin/api-docs" || request.URL.Path == "/admin/apidocs.html"
		hxcPage := request.URL.Path == "/admin/hxc-dashboard"
		aiAssistantPage := request.URL.Path == "/admin/ai.html" || request.URL.Path == "/admin/aiDetail.html" || request.URL.Path == "/admin/cloud-orchestrator/plans" || strings.HasPrefix(request.URL.Path, "/admin/cloud-orchestrator/plans/")
		if (request.URL.Path == "/admin/campaigns.html" && externaleffects.ValidUIQuery(request.URL.Query())) || hxcPage || mediaPage || tagsPage || productPage || orderPage || couponPage || groupOpsPage || automationPage || surveyPage || operationCyclesPage || configPage || aiAssistantPage {
			styleSource = "'self' 'unsafe-inline'"
		}
		imageSource := "'self' data:"
		if mediaPage {
			// The frozen Media controller creates private thumbnail object URLs;
			// keep blob: limited to the three v3-owned Media shell routes.
			imageSource += " blob:"
		}
		contentPolicy := "default-src 'self'; script-src 'self' https://res.wx.qq.com; style-src " + styleSource + "; img-src " + imageSource + "; font-src 'self'; connect-src 'self'; object-src 'none'; base-uri 'none'; form-action 'self'"
		if request.URL.Path != webshell.SidebarPagePath && !strings.HasPrefix(request.URL.Path, "/api/sidebar/") {
			writer.Header().Set("X-Frame-Options", "SAMEORIGIN")
			contentPolicy += "; frame-ancestors 'self'"
		}
		writer.Header().Set("Content-Security-Policy", contentPolicy)
		next.ServeHTTP(writer, request)
	})
}

func isProductShellPath(path string) bool {
	if strings.HasSuffix(path, "/spProductData.html") {
		return false
	}
	if strings.HasPrefix(path, "/admin/wechat-pay/products") || strings.HasPrefix(path, "/admin/service-period-products") {
		return true
	}
	switch path {
	case "/admin/products.html", "/admin/productForm.html", "/admin/spProducts.html", "/admin/spProductForm.html", "/admin/wechat-pay/spProducts.html", "/admin/wechat-pay/spProductForm.html":
		return true
	default:
		return false
	}
}
