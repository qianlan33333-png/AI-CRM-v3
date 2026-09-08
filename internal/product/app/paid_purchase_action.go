package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"sort"
	"strings"
	"time"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
)

// PaidPurchaseActionStore keeps the payment fact separate from Product's
// ordinary admin command store. Every method requires the active transaction:
// the action snapshot, the Customer tag command and any EER acceptance either
// commit together with the Order settlement or all roll back.
type PaidPurchaseActionStore interface {
	GetForUpdate(context.Context, productport.ID) (productport.Product, error)
	ReadPaidPurchaseAction(context.Context, int64) (productport.PaidPurchaseAction, error)
	ReadPaidPurchaseActionForUpdate(context.Context, int64) (productport.PaidPurchaseAction, error)
	CreatePaidPurchaseAction(context.Context, productport.PaidPurchaseAction, []int64) (productport.PaidPurchaseAction, bool, error)
	CompletePaidPurchaseActionTag(context.Context, int64, int64, string) (productport.PaidPurchaseAction, error)
}

type PaidPurchaseActionService struct {
	uow   platformport.UnitOfWork
	store PaidPurchaseActionStore
	tags  customerport.TagCommandSubmitter
	now   func() time.Time
}

func NewPaidPurchaseActionService(uow platformport.UnitOfWork, store PaidPurchaseActionStore, tags customerport.TagCommandSubmitter) (*PaidPurchaseActionService, error) {
	if uow == nil || store == nil || tags == nil {
		return nil, ErrUnavailable
	}
	return &PaidPurchaseActionService{uow: uow, store: store, tags: tags, now: time.Now}, nil
}

// ConsumePaidEventWithin is the Product side of Order's first-paid fan-out.
// It reads the current Product definition under lock, freezes its safe buyer
// action, and reuses Customer's existing tag-command + External Effects path.
// It does not resolve/provision a Customer or call a Provider.
func (s *PaidPurchaseActionService) ConsumePaidEventWithin(ctx context.Context, event orderport.PaidEvent) error {
	if s == nil || s.store == nil || s.tags == nil || s.now == nil || !event.Valid() {
		return ErrUnavailable
	}
	if _, err := platformpostgres.RequireTransaction(ctx); err != nil {
		return err
	}
	// A native order created outside the public Product checkout has no frozen
	// Product target. It remains a valid paid Order but has no Product-owned
	// post-purchase action to infer.
	if event.CheckoutProductID < 1 {
		return nil
	}
	// The immutable snapshot is the authority for a replay. In particular, do
	// not re-read a later Product edit or reject an old paid event because its
	// current definition became invalid after the first settlement.
	if stored, readErr := s.store.ReadPaidPurchaseActionForUpdate(ctx, event.ID); readErr == nil {
		if !samePaidPurchaseEvent(stored, event) {
			return ErrConflict
		}
		return nil
	} else if !errors.Is(readErr, productport.ErrProductReadNotFound) {
		return classify(readErr)
	}
	product, err := s.store.GetForUpdate(ctx, productport.ID(event.CheckoutProductID))
	if err != nil {
		return classify(err)
	}
	if !validProduct(product) {
		return ErrUnavailable
	}
	configuration, err := paidPurchaseConfigFromProjection(product.LegacyAdminProjection)
	if err != nil {
		return err
	}
	createdAt := event.OccurredAt.UTC()
	if createdAt.IsZero() {
		createdAt = s.now().UTC()
	}
	action := productport.PaidPurchaseAction{
		OrderPaidEventID: event.ID,
		OrderID:          event.OrderID,
		ProductID:        product.ID,
		ProductVersion:   product.Version,
		SourceDigest:     event.SourceDigest,
		Enabled:          configuration.Enabled,
		Mode:             configuration.Mode,
		LeadChannelID:    configuration.LeadChannelID,
		LeadQRTitle:      configuration.LeadQRTitle,
		LeadQRSubtitle:   configuration.LeadQRSubtitle,
		RedirectURL:      configuration.RedirectURL,
		TagState:         configuration.TagState,
		CreatedAt:        createdAt,
	}
	stored, created, err := s.store.CreatePaidPurchaseAction(ctx, action, configuration.TagIDs)
	if err != nil {
		return classify(err)
	}
	if !created {
		if !samePaidPurchaseEvent(stored, event) {
			return ErrConflict
		}
		return nil
	}
	if len(configuration.TagIDs) == 0 {
		return nil
	}
	if event.Order.PayerCustomerID == nil || *event.Order.PayerCustomerID < 1 {
		_, err = s.store.CompletePaidPurchaseActionTag(ctx, event.ID, 0, "skipped_customer_unavailable")
		return classify(err)
	}
	result, err := s.tags.SubmitTagCommandWithin(ctx, customerport.TagCommand{
		Source:         "product_paid_purchase",
		SourceRef:      paidPurchaseTagSourceRef(event.ID),
		IdempotencyKey: paidPurchaseTagSourceRef(event.ID),
		Targets: []customerport.TagCommandTarget{{
			CustomerID: customerdomain.CustomerID(*event.Order.PayerCustomerID),
			AddTagIDs:  slices.Clone(configuration.TagIDs),
		}},
		OccurredAt: createdAt,
	})
	if err != nil {
		return err
	}
	state := paidPurchaseTagState(result)
	if _, err = s.store.CompletePaidPurchaseActionTag(ctx, event.ID, result.ID, state); err != nil {
		return classify(err)
	}
	return nil
}

// ReadPaidPurchaseAction is Product's narrow read side. Payment verifies the
// trusted session and paid checkout before it can call this method.
func (s *PaidPurchaseActionService) ReadPaidPurchaseAction(ctx context.Context, orderID int64) (productport.PaidPurchaseAction, error) {
	if s == nil || s.uow == nil || s.store == nil || orderID < 1 {
		return productport.PaidPurchaseAction{}, productport.ErrProductReadUnavailable
	}
	var result productport.PaidPurchaseAction
	err := s.uow.Within(ctx, func(tx context.Context) error {
		var readErr error
		result, readErr = s.store.ReadPaidPurchaseAction(tx, orderID)
		return readErr
	})
	if err != nil {
		return productport.PaidPurchaseAction{}, classify(err)
	}
	if !validPaidPurchaseAction(result) {
		return productport.PaidPurchaseAction{}, productport.ErrProductReadUnavailable
	}
	return result, nil
}

type paidPurchaseConfiguration struct {
	Enabled                                  bool
	Mode                                     productport.PaidPurchaseActionMode
	LeadChannelID                            int64
	LeadQRTitle, LeadQRSubtitle, RedirectURL string
	TagIDs                                   []int64
	TagState                                 string
}

func paidPurchaseConfigFromProjection(raw json.RawMessage) (paidPurchaseConfiguration, error) {
	canonical, err := CanonicalLegacyAdminProjection(raw)
	if err != nil {
		return paidPurchaseConfiguration{}, ErrUnavailable
	}
	var projection struct {
		Enabled               bool            `json:"purchase_action_enabled"`
		Mode                  string          `json:"purchase_action_mode"`
		LeadChannelID         *int64          `json:"lead_channel_id"`
		LeadQRTitle           string          `json:"lead_qr_title"`
		LeadQRSubtitle        string          `json:"lead_qr_subtitle"`
		CompletionRedirectURL string          `json:"completion_redirect_url"`
		Tagging               json.RawMessage `json:"wecom_tagging"`
	}
	if json.Unmarshal(canonical, &projection) != nil {
		return paidPurchaseConfiguration{}, ErrUnavailable
	}
	result := paidPurchaseConfiguration{Mode: productport.PaidPurchaseActionNone, TagState: "not_configured"}
	if projection.Enabled {
		result.Enabled, result.LeadQRTitle, result.LeadQRSubtitle = true, projection.LeadQRTitle, projection.LeadQRSubtitle
		switch projection.Mode {
		case string(productport.PaidPurchaseActionQR):
			if projection.LeadChannelID == nil || *projection.LeadChannelID < 1 {
				return paidPurchaseConfiguration{}, ErrInvalidProduct
			}
			result.Mode, result.LeadChannelID = productport.PaidPurchaseActionQR, *projection.LeadChannelID
		case string(productport.PaidPurchaseActionRedirect):
			if !validPaidPurchaseRedirect(projection.CompletionRedirectURL) {
				return paidPurchaseConfiguration{}, ErrInvalidProduct
			}
			result.Mode, result.RedirectURL = productport.PaidPurchaseActionRedirect, projection.CompletionRedirectURL
		default:
			return paidPurchaseConfiguration{}, ErrInvalidProduct
		}
	}
	if tags, ok := paidPurchaseTagIDs(projection.Tagging); ok {
		result.TagIDs = tags
		if len(tags) > 0 {
			result.TagState = "queued"
		}
	} else {
		// QR/redirect availability and customer tagging are independently
		// controlled. A disabled tag section must not borrow the purchase
		// presentation switch for its persisted state.
		result.TagState = "disabled"
	}
	return result, nil
}

func paidPurchaseTagIDs(raw json.RawMessage) ([]int64, bool) {
	var config struct {
		Enabled bool    `json:"enabled"`
		TagIDs  []int64 `json:"tag_ids"`
	}
	if len(raw) == 0 || json.Unmarshal(raw, &config) != nil || !config.Enabled || len(config.TagIDs) == 0 || len(config.TagIDs) > 100 {
		return nil, false
	}
	sort.Slice(config.TagIDs, func(i, j int) bool { return config.TagIDs[i] < config.TagIDs[j] })
	for index, id := range config.TagIDs {
		if id < 1 || index > 0 && config.TagIDs[index-1] == id {
			return nil, false
		}
	}
	return slices.Clone(config.TagIDs), true
}

func validPaidPurchaseRedirect(value string) bool {
	if value == "" || value != strings.TrimSpace(value) || len(value) > 2048 || strings.ContainsAny(value, "\\\r\n\t") {
		return false
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.User != nil || parsed.Fragment != "" {
		return false
	}
	if parsed.Scheme == "https" && parsed.Host != "" {
		return true
	}
	return parsed.Scheme == "" && parsed.Host == "" && strings.HasPrefix(parsed.Path, "/") && !strings.HasPrefix(value, "//")
}

func paidPurchaseTagSourceRef(eventID int64) string {
	return fmt.Sprintf("paid-event:%d", eventID)
}

func paidPurchaseTagState(result customerport.TagCommandResult) string {
	if result.State == "queued" {
		return "queued"
	}
	if result.State == "partial" {
		return "partial"
	}
	if result.State == "outcome_unknown" {
		return "outcome_unknown"
	}
	return "skipped_target_unavailable"
}

func samePaidPurchaseEvent(action productport.PaidPurchaseAction, event orderport.PaidEvent) bool {
	return action.OrderPaidEventID == event.ID && action.OrderID == event.OrderID && action.ProductID == productport.ID(event.CheckoutProductID) && action.SourceDigest == event.SourceDigest
}

func validPaidPurchaseAction(value productport.PaidPurchaseAction) bool {
	if value.OrderPaidEventID < 1 || value.OrderID < 1 || value.ProductID < 1 || value.ProductVersion < 1 || value.SourceDigest == ([32]byte{}) || value.CreatedAt.IsZero() {
		return false
	}
	switch value.Mode {
	case productport.PaidPurchaseActionNone:
		return !value.Enabled && value.LeadChannelID == 0 && value.RedirectURL == "" && paidPurchaseTagStateValid(value.TagState)
	case productport.PaidPurchaseActionQR:
		return value.Enabled && value.LeadChannelID > 0 && value.RedirectURL == "" && paidPurchaseTagStateValid(value.TagState)
	case productport.PaidPurchaseActionRedirect:
		return value.Enabled && value.LeadChannelID == 0 && validPaidPurchaseRedirect(value.RedirectURL) && paidPurchaseTagStateValid(value.TagState)
	default:
		return false
	}
}

func paidPurchaseTagStateValid(value string) bool {
	return value == "disabled" || value == "not_configured" || value == "queued" || value == "partial" || value == "outcome_unknown" || value == "skipped_customer_unavailable" || value == "skipped_target_unavailable"
}

var _ orderport.PaidEventConsumer = (*PaidPurchaseActionService)(nil)
var _ productport.PaidPurchaseActionReader = (*PaidPurchaseActionService)(nil)
