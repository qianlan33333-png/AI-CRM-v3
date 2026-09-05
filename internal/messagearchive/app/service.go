package app

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	accessport "github.com/qianlan33333-png/AI-CRM-v3/internal/access/port"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/messagearchive/domain"
	archiveport "github.com/qianlan33333-png/AI-CRM-v3/internal/messagearchive/port"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	wecomport "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
)

var (
	ErrCursorAdvanced = errors.New("message archive cursor advanced concurrently")
	ErrProviderPage   = errors.New("message archive provider page invalid")
)

type Store interface {
	CommittedCursor(context.Context, string) (uint64, error)
	StartRun(context.Context, SyncRun) (int64, error)
	CommitBatch(context.Context, Batch) (BatchResult, error)
	RecordBlockedIssue(context.Context, string, IngestIssue, time.Time) error
	FinishRun(context.Context, int64, SyncRunFinish) error
	CustomerMessages(context.Context, archiveport.CustomerQuery) (archiveport.CustomerPage, error)
	CustomerStaffIDs(context.Context, []customerdomain.CustomerID) ([]int64, error)
	MediaAccess(context.Context, MediaQuery) (MediaReference, error)
}

type MediaQuery struct {
	CustomerIDs []customerdomain.CustomerID
	MediaID     int64
}

type MediaReference struct {
	Kind            string
	ProviderFileRef string
	ExpectedMD5     string
	ExpectedSize    int64
	HasExpectedSize bool
}

type SyncRun struct {
	CorpScope         string
	Trigger           string
	WebhookDeliveryID int64
	StartSeq          uint64
	StartedAt         time.Time
}
type SyncRunFinish struct {
	EndSeq     uint64
	Status     string
	ErrorCode  string
	FinishedAt time.Time
}
type IngestIssue struct {
	Seq     uint64
	MsgID   string
	Stage   string
	Reason  string
	Payload []byte
	Digest  [32]byte
}
type Batch struct {
	CorpScope        string
	ExpectedCursor   uint64
	EndSeq           uint64
	RunID            int64
	Messages         []domain.Message
	Issues           []IngestIssue
	NotifyReceivedAt time.Time
}
type BatchResult struct {
	Inserted        int
	Duplicates      int
	Unresolved      int
	Issues          int
	CommittedCursor uint64
}

type StaffReader interface {
	UserByWeComUserID(context.Context, string, bool) (accessdomain.User, error)
}

// Service drains only an already accepted Inbox notification.  It contains no
// ticker, job registration, provider write or own retry loop.  A caller must
// invoke it from the existing durable Webhook Inbox processor.
type Service struct {
	Enabled        bool // Provider notification and SDK ingestion switch.
	ReadEnabled    bool // Local archive read-model switch; independent of Provider SDK.
	CorpScope      string
	Reader         wecomport.MessageArchiveReader
	Identity       identityport.Resolver
	Lineage        identityport.CanonicalLineageReader
	Staff          StaffReader
	StaffDirectory accessport.MessageArchiveStaffDirectory
	Store          Store
	UOW            platformport.UnitOfWork
	PageLimit      uint32
	PageBudget     int
	Now            func() time.Time
}

var _ archiveport.InboxDeliveryHandler = Service{}

func (service Service) ProcessArchiveDelivery(ctx context.Context, delivery archiveport.InboxDelivery) (resultErr error) {
	if err := service.valid(); err != nil {
		return err
	}
	_, err := decodeNotification(delivery.Payload, service.CorpScope)
	if err != nil {
		return err
	}
	if delivery.ReceivedAt.IsZero() {
		return ErrProviderPage
	}
	started := service.now().UTC()
	cursor, err := service.cursor(ctx)
	if err != nil {
		return err
	}
	runID, err := service.startRun(ctx, delivery, cursor, started)
	if err != nil {
		return err
	}
	endCursor := cursor
	finish := func(status, code string) {
		finishCtx := context.Background()
		_ = service.UOW.Within(finishCtx, func(tx context.Context) error {
			return service.Store.FinishRun(tx, runID, SyncRunFinish{EndSeq: endCursor, Status: status, ErrorCode: code, FinishedAt: service.now().UTC()})
		})
	}
	defer func() {
		if resultErr != nil {
			finish("failed", safeErrorCode(resultErr))
		}
	}()

	for page := 0; page < service.PageBudget; page++ {
		encrypted, fetchErr := service.Reader.GetChatData(ctx, cursor, service.PageLimit)
		if fetchErr != nil {
			return fetchErr
		}
		if len(encrypted) == 0 {
			if err = service.UOW.Within(ctx, func(tx context.Context) error {
				return service.Store.FinishRun(tx, runID, SyncRunFinish{EndSeq: cursor, Status: "succeeded", FinishedAt: service.now().UTC()})
			}); err != nil {
				return err
			}
			return nil
		}
		plain, decryptErr := service.Reader.DecryptArchiveData(ctx, encrypted)
		if decryptErr != nil {
			return decryptErr
		}
		if len(plain) != len(encrypted) {
			return ErrProviderPage
		}
		messages, issues, trusted, next, normalizeErr := service.normalizePage(encrypted, plain)
		if normalizeErr != nil {
			if len(issues) == 1 {
				if recordErr := service.recordBlockedIssue(ctx, issues[0]); recordErr != nil {
					return recordErr
				}
			}
			return normalizeErr
		}
		// Database identity resolution and message/cursor persistence share the
		// same Unit of Work. SDK work completed above and no network call runs
		// while this transaction is held.
		var committed BatchResult
		err = service.UOW.Within(ctx, func(tx context.Context) error {
			if resolveErr := service.resolveParticipants(tx, messages, trusted); resolveErr != nil {
				return resolveErr
			}
			var commitErr error
			committed, commitErr = service.Store.CommitBatch(tx, Batch{CorpScope: service.CorpScope, ExpectedCursor: cursor, EndSeq: next,
				RunID: runID, Messages: messages, Issues: issues, NotifyReceivedAt: delivery.ReceivedAt.UTC()})
			return commitErr
		})
		if errors.Is(err, ErrCursorAdvanced) {
			cursor, err = service.cursor(ctx)
			if err != nil {
				return err
			}
			endCursor = cursor
			continue
		}
		if err != nil {
			return err
		}
		cursor, endCursor = committed.CommittedCursor, committed.CommittedCursor
	}
	return archiveport.ErrWorkBudgetExceeded
}

func (service Service) normalizePage(encrypted []wecomport.EncryptedArchiveRecord, plain []wecomport.PlainArchiveRecord) ([]domain.Message, []IngestIssue, map[string]identitydomain.VerifiedFact, uint64, error) {
	messages := make([]domain.Message, 0, len(plain))
	issues := make([]IngestIssue, 0)
	trusted := make(map[string]identitydomain.VerifiedFact)
	var next uint64
	for index := range encrypted {
		if encrypted[index].Seq == 0 || encrypted[index].Seq != plain[index].Seq || encrypted[index].MsgID != plain[index].MsgID {
			return nil, nil, nil, 0, ErrProviderPage
		}
		if encrypted[index].Seq > next {
			next = encrypted[index].Seq
		}
		message, err := NormalizeArchiveRecord(service.CorpScope, plain[index])
		if err != nil {
			// A malformed record makes this page non-committable. The caller
			// stores the exact protected bytes and retries from the same cursor;
			// it must never skip forward behind an opaque digest.
			payload, digest := domain.NewIssuePayload(plain[index].Payload)
			return nil, []IngestIssue{{Seq: plain[index].Seq, MsgID: plain[index].MsgID, Stage: "normalize", Reason: "message_invalid", Payload: payload, Digest: digest}}, nil, 0, ErrProviderPage
		}
		attachTrustedParticipants(&message, plain[index].ExternalIdentities, trusted)
		messages = append(messages, message)
	}
	if next == 0 {
		return nil, nil, nil, 0, ErrProviderPage
	}
	return messages, issues, trusted, next, nil
}

func (service Service) resolveParticipants(ctx context.Context, messages []domain.Message, trusted map[string]identitydomain.VerifiedFact) error {
	// This cache is deliberately scoped to one SDK page and one UoW. It avoids
	// N+1 read pressure without persisting identity observations, modifying OneID,
	// or carrying a verified fact into a later notification.
	type staffResolution struct {
		user     accessdomain.User
		notFound bool
	}
	staffCache := make(map[string]staffResolution)
	identityCache := make(map[string]identityport.ResolveResult)
	for messageIndex := range messages {
		for participantIndex := range messages[messageIndex].Participants {
			participant := &messages[messageIndex].Participants[participantIndex]
			now := service.now().UTC()
			switch participant.ActorType {
			case domain.ActorRobot:
				participant.ResolutionStatus, participant.ResolutionReason = domain.ResolutionNotApplicable, "robot"
			case domain.ActorStaff:
				resolved, cached := staffCache[participant.ProviderValue]
				if !cached {
					user, err := service.Staff.UserByWeComUserID(ctx, participant.ProviderValue, false)
					if errors.Is(err, accessdomain.ErrNotFound) {
						resolved = staffResolution{notFound: true}
					} else if err != nil {
						return err
					} else {
						resolved = staffResolution{user: user}
					}
					staffCache[participant.ProviderValue] = resolved
				}
				if resolved.notFound {
					// No prefix or HTTP input is enough to call a participant staff.
					participant.ResolutionStatus, participant.ResolutionReason = domain.ResolutionNotFound, "provider_actor_unknown"
					continue
				}
				participant.StaffUserID, participant.ResolutionStatus, participant.ResolutionReason, participant.ResolvedAt = resolved.user.ID, domain.ResolutionNotApplicable, "", &now
			case domain.ActorExternal:
				fact, trusted := trusted[participant.ProviderValue]
				if !trusted || !fact.Valid() {
					participant.ResolutionStatus, participant.ResolutionReason = domain.ResolutionNotFound, "provider_actor_unknown"
					continue
				}
				ref := fact.Reference()
				cacheKey := string(ref.Kind) + "\x00" + ref.Scope + "\x00" + ref.NormalizedValue + "\x00" + string(ref.Assurance) + "\x00" + ref.Source
				resolved, cached := identityCache[cacheKey]
				if !cached {
					var err error
					resolved, err = service.Identity.Resolve(ctx, identitydomain.Reference{Kind: ref.Kind, Scope: ref.Scope, Value: ref.NormalizedValue, Assurance: ref.Assurance, Source: ref.Source})
					if err != nil {
						return err
					}
					identityCache[cacheKey] = resolved
				}
				participant.ResolvedAt = &now
				switch resolved.Status {
				case identityport.ResolveFound:
					participant.CustomerID, participant.IdentityID, participant.ResolutionStatus, participant.ResolutionReason = resolved.CustomerID, resolved.IdentityID, domain.ResolutionFound, ""
				case identityport.ResolveNotFound:
					participant.ResolutionStatus, participant.ResolutionReason = domain.ResolutionNotFound, "oneid_not_found"
				case identityport.ResolveConflict:
					participant.ResolutionStatus, participant.ResolutionReason = domain.ResolutionConflict, "oneid_conflict"
				default:
					return ErrProviderPage
				}
			default:
				participant.ResolutionStatus, participant.ResolutionReason = domain.ResolutionNotApplicable, "provider_actor_unknown"
			}
		}
	}
	return nil
}

func (service Service) cursor(ctx context.Context) (uint64, error) {
	var value uint64
	err := service.UOW.Within(ctx, func(tx context.Context) error {
		var err error
		value, err = service.Store.CommittedCursor(tx, service.CorpScope)
		return err
	})
	return value, err
}
func (service Service) startRun(ctx context.Context, delivery archiveport.InboxDelivery, cursor uint64, started time.Time) (int64, error) {
	var id int64
	err := service.UOW.Within(ctx, func(tx context.Context) error {
		var err error
		id, err = service.Store.StartRun(tx, SyncRun{CorpScope: service.CorpScope, Trigger: "notify", WebhookDeliveryID: delivery.ID, StartSeq: cursor, StartedAt: started})
		return err
	})
	return id, err
}
func (service Service) valid() error {
	if !service.Enabled || service.Reader == nil || service.Identity == nil || service.Lineage == nil || service.Staff == nil || service.Store == nil || service.UOW == nil || !strings.HasPrefix(service.CorpScope, "wecom-corp:") || service.PageLimit < 1 || service.PageLimit > 1000 || service.PageBudget < 1 || service.PageBudget > 1000 {
		return archiveport.ErrNotReady
	}
	return nil
}
func (service Service) now() time.Time {
	if service.Now != nil {
		return service.Now()
	}
	return time.Now()
}
func safeErrorCode(err error) string {
	switch {
	case errors.Is(err, archiveport.ErrWorkBudgetExceeded):
		return "work_budget_exhausted"
	case errors.Is(err, ErrProviderPage):
		return "provider_page_invalid"
	default:
		return "archive_processing_failed"
	}
}

// CustomerMessages queries the current OneID lineage at read time. It never
// mutates customer_id_at_ingest, so merge reversals naturally change only the
// view rather than rewriting chat evidence.
// ReadPrivateMedia authorizes the media against the requested customer's
// current OneID lineage in PostgreSQL, then reads bounded SDK chunks outside
// that transaction. It is an explicit authenticated read, never a background
// GetChatData poll.
func (service Service) ReadPrivateMedia(ctx context.Context, customerID customerdomain.CustomerID, mediaID int64) (archiveport.MediaContent, error) {
	if !service.Enabled || service.Lineage == nil || service.Store == nil || service.UOW == nil || service.Reader == nil || customerID < 1 || mediaID < 1 {
		return archiveport.MediaContent{}, archiveport.ErrNotReady
	}
	var reference MediaReference
	err := service.UOW.Within(ctx, func(tx context.Context) error {
		lineage, lineageErr := service.Lineage.CanonicalLineage(tx, customerID)
		if lineageErr != nil {
			return lineageErr
		}
		var readErr error
		reference, readErr = service.Store.MediaAccess(tx, MediaQuery{CustomerIDs: lineage, MediaID: mediaID})
		return readErr
	})
	if err != nil {
		return archiveport.MediaContent{}, err
	}
	if reference.ProviderFileRef == "" || reference.Kind == "" {
		return archiveport.MediaContent{}, ErrProviderPage
	}
	const maxMediaBytes = 32 << 20
	const maxMediaChunks = 128
	var data bytes.Buffer
	index := ""
	for count := 0; count < maxMediaChunks; count++ {
		chunk, chunkErr := service.Reader.GetArchiveMedia(ctx, wecomport.ArchiveMediaRequest{FileID: reference.ProviderFileRef, IndexBuf: index})
		if chunkErr != nil {
			return archiveport.MediaContent{}, chunkErr
		}
		if len(chunk.Data) > maxMediaBytes-data.Len() {
			return archiveport.MediaContent{}, ErrProviderPage
		}
		_, _ = data.Write(chunk.Data)
		if chunk.Finished {
			if reference.HasExpectedSize && int64(data.Len()) != reference.ExpectedSize {
				return archiveport.MediaContent{}, ErrProviderPage
			}
			if reference.ExpectedMD5 != "" {
				digest := md5.Sum(data.Bytes())
				if !strings.EqualFold(reference.ExpectedMD5, hex.EncodeToString(digest[:])) {
					return archiveport.MediaContent{}, ErrProviderPage
				}
			}
			return archiveport.MediaContent{Kind: reference.Kind, Data: append([]byte(nil), data.Bytes()...)}, nil
		}
		if chunk.NextIndexBuf == "" || chunk.NextIndexBuf == index {
			return archiveport.MediaContent{}, ErrProviderPage
		}
		index = chunk.NextIndexBuf
	}
	return archiveport.MediaContent{}, ErrProviderPage
}

func (service Service) CustomerMessages(ctx context.Context, query archiveport.CustomerQuery) (archiveport.CustomerPage, error) {
	if !service.ReadEnabled || service.Lineage == nil || service.Store == nil || service.StaffDirectory == nil || service.UOW == nil || query.CustomerID < 1 {
		return archiveport.CustomerPage{}, archiveport.ErrNotReady
	}
	var page archiveport.CustomerPage
	err := service.UOW.Within(ctx, func(tx context.Context) error {
		lineage, err := service.Lineage.CanonicalLineage(tx, query.CustomerID)
		if err != nil {
			return err
		}
		query.CustomerIDs = lineage
		var readErr error
		page, readErr = service.Store.CustomerMessages(tx, query)
		if readErr != nil {
			return readErr
		}
		return service.populateStaffNames(tx, &page)
	})
	return page, err
}

// CustomerStaff uses the same canonical lineage and local read gate as the
// message page.  The option list is limited to staff already represented in
// this customer's archive, so it does not become a general employee directory.
func (service Service) CustomerStaff(ctx context.Context, customerID customerdomain.CustomerID) ([]archiveport.StaffOption, error) {
	if !service.ReadEnabled || service.Lineage == nil || service.Store == nil || service.StaffDirectory == nil || service.UOW == nil || customerID < 1 {
		return nil, archiveport.ErrNotReady
	}
	var options []archiveport.StaffOption
	err := service.UOW.Within(ctx, func(tx context.Context) error {
		lineage, err := service.Lineage.CanonicalLineage(tx, customerID)
		if err != nil {
			return err
		}
		ids, readErr := service.Store.CustomerStaffIDs(tx, lineage)
		if readErr != nil {
			return readErr
		}
		staff, readErr := service.StaffDirectory.MessageArchiveStaff(tx, ids)
		if readErr != nil {
			return readErr
		}
		options = make([]archiveport.StaffOption, 0, len(staff))
		for _, item := range staff {
			options = append(options, archiveport.StaffOption{ID: item.ID, DisplayName: item.DisplayName})
		}
		return nil
	})
	return options, err
}

func (service Service) populateStaffNames(ctx context.Context, page *archiveport.CustomerPage) error {
	if page == nil || len(page.Items) == 0 {
		return nil
	}
	ids := make([]int64, 0)
	for _, item := range page.Items {
		ids = append(ids, item.StaffIDs...)
	}
	staff, err := service.StaffDirectory.MessageArchiveStaff(ctx, ids)
	if err != nil {
		return err
	}
	names := make(map[int64]string, len(staff))
	for _, item := range staff {
		names[item.ID] = item.DisplayName
	}
	for index := range page.Items {
		page.Items[index].StaffNames = page.Items[index].StaffNames[:0]
		for _, id := range page.Items[index].StaffIDs {
			if name, found := names[id]; found {
				page.Items[index].StaffNames = append(page.Items[index].StaffNames, name)
			}
		}
	}
	return nil
}

type notificationPayload struct {
	CorpID string `json:"corp_id"`
	Event  string `json:"event"`
}

func decodeNotification(raw []byte, scope string) (notificationPayload, error) {
	var payload notificationPayload
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		return notificationPayload{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return notificationPayload{}, ErrProviderPage
	}
	if payload.CorpID == "" || scope != "wecom-corp:"+payload.CorpID || payload.Event != "msgaudit_notify" {
		return notificationPayload{}, ErrProviderPage
	}
	return payload, nil
}

// attachTrustedParticipants changes only exact participant values attested by
// the trusted WeCom reader for this decrypted record. It never derives a
// verified identity from request/import data or from a generic string prefix.
func attachTrustedParticipants(message *domain.Message, identities []wecomport.TrustedArchiveExternalIdentity, trusted map[string]identitydomain.VerifiedFact) {
	if message == nil {
		return
	}
	for _, identity := range identities {
		if identity.Value == "" || !identity.Fact.Valid() {
			continue
		}
		for index := range message.Participants {
			participant := &message.Participants[index]
			if participant.ProviderValue != identity.Value || participant.ActorType == domain.ActorRobot {
				continue
			}
			participant.ActorType = domain.ActorExternal
			participant.ResolutionStatus = domain.ResolutionNotFound
			participant.ResolutionReason = "oneid_not_found"
			trusted[identity.Value] = identity.Fact
		}
	}
}

func (service Service) recordBlockedIssue(ctx context.Context, issue IngestIssue) error {
	return service.UOW.Within(ctx, func(tx context.Context) error {
		return service.Store.RecordBlockedIssue(tx, service.CorpScope, issue, service.now().UTC())
	})
}
