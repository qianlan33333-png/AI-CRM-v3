package app

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
)

const (
	DefaultLimit  = 50
	MaximumLimit  = 200
	ExactCountCap = 10000
)

var (
	ErrInvalidQuery  = errors.New("invalid customer directory query")
	ErrInvalidCursor = errors.New("invalid customer directory cursor")
	ErrNotFound      = errors.New("customer directory record not found")
)

type Filters struct {
	Keyword          string
	Status           string
	ActivationStatus string
	PhoneCustomerID  customerdomain.CustomerID
	PhoneMatchNone   bool
}

type Query struct {
	Filters   Filters
	Limit     int
	Watermark time.Time
	AfterAt   time.Time
	AfterID   customerdomain.CustomerID
}

type Item struct {
	CustomerID      customerdomain.CustomerID `json:"customer_id"`
	CustomerStatus  customerdomain.Status     `json:"status"`
	DisplayName     string                    `json:"display_name"`
	AvatarURL       string                    `json:"avatar_url"`
	OneIDLabel      string                    `json:"oneid"`
	PhoneMasked     string                    `json:"phone_masked"`
	PhoneAssurance  string                    `json:"phone_assurance,omitempty"`
	ActivationState string                    `json:"activation_status"`
	LastSyncedAt    *time.Time                `json:"last_synced_at,omitempty"`
	UpdatedAt       time.Time                 `json:"updated_at"`
}

type Detail struct {
	Item
	Gender      int16  `json:"gender"`
	ContactType int16  `json:"contact_type"`
	CorpName    string `json:"corp_name"`
	Source      string `json:"source"`
}

type PageData struct {
	Items           []Item
	Count           int64
	TotalIsEstimate bool
}

type Page struct {
	Items           []Item    `json:"items"`
	NextCursor      string    `json:"next_cursor,omitempty"`
	Total           int64     `json:"total"`
	TotalIsEstimate bool      `json:"total_is_estimate"`
	Watermark       time.Time `json:"watermark"`
}

type Store interface {
	List(context.Context, Query) (PageData, error)
	Detail(context.Context, customerdomain.CustomerID) (Detail, error)
}

type Directory struct {
	Store      Store
	Now        func() time.Time
	SigningKey []byte
}

type ListRequest struct {
	Filters Filters
	Limit   int
	Cursor  string
}

type cursorPayload struct {
	Version    int    `json:"v"`
	Watermark  string `json:"w"`
	AfterAt    string `json:"a"`
	AfterID    int64  `json:"i"`
	FilterHash string `json:"f"`
}

func (directory Directory) List(ctx context.Context, request ListRequest) (Page, error) {
	request.Filters.Keyword = strings.TrimSpace(request.Filters.Keyword)
	if len(request.Filters.Keyword) > 200 || !validStatus(request.Filters.Status) || !validActivation(request.Filters.ActivationStatus) || request.Limit < 0 || request.Limit > MaximumLimit {
		return Page{}, ErrInvalidQuery
	}
	if len(directory.SigningKey) < 32 {
		return Page{}, ErrInvalidCursor
	}
	if request.Limit == 0 {
		request.Limit = DefaultLimit
	}
	hash := filtersHash(request.Filters)
	watermark := time.Now().UTC()
	if directory.Now != nil {
		watermark = directory.Now().UTC()
	}
	query := Query{Filters: request.Filters, Limit: request.Limit + 1, Watermark: watermark}
	if request.Cursor != "" {
		payload, err := decodeCursor(request.Cursor, directory.SigningKey)
		if err != nil || payload.FilterHash != hash || payload.AfterID < 1 {
			return Page{}, ErrInvalidCursor
		}
		query.Watermark, err = time.Parse(time.RFC3339Nano, payload.Watermark)
		if err != nil {
			return Page{}, ErrInvalidCursor
		}
		query.AfterAt, err = time.Parse(time.RFC3339Nano, payload.AfterAt)
		if err != nil || query.AfterAt.After(query.Watermark) {
			return Page{}, ErrInvalidCursor
		}
		query.AfterID = customerdomain.CustomerID(payload.AfterID)
	}
	data, err := directory.Store.List(ctx, query)
	if err != nil {
		return Page{}, err
	}
	page := Page{Items: data.Items, Total: data.Count, TotalIsEstimate: data.TotalIsEstimate, Watermark: query.Watermark}
	if len(page.Items) > request.Limit {
		last := page.Items[request.Limit-1]
		page.Items = page.Items[:request.Limit]
		page.NextCursor, err = encodeCursor(cursorPayload{Version: 1, Watermark: query.Watermark.Format(time.RFC3339Nano), AfterAt: last.UpdatedAt.Format(time.RFC3339Nano), AfterID: int64(last.CustomerID), FilterHash: hash}, directory.SigningKey)
		if err != nil {
			return Page{}, err
		}
	}
	return page, nil
}

func filtersHash(filters Filters) string {
	payload, _ := json.Marshal([]any{filters.Keyword, filters.Status, filters.ActivationStatus, filters.PhoneCustomerID, filters.PhoneMatchNone})
	digest := sha256.Sum256(payload)
	return base64.RawURLEncoding.EncodeToString(digest[:])
}

func encodeCursor(payload cursorPayload, key []byte) (string, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(raw)
	return base64.RawURLEncoding.EncodeToString(raw) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

func decodeCursor(value string, key []byte) (cursorPayload, error) {
	if len(value) > 2048 {
		return cursorPayload{}, ErrInvalidCursor
	}
	parts := strings.Split(value, ".")
	if len(parts) != 2 {
		return cursorPayload{}, ErrInvalidCursor
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return cursorPayload{}, ErrInvalidCursor
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return cursorPayload{}, ErrInvalidCursor
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(raw)
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return cursorPayload{}, ErrInvalidCursor
	}
	var payload cursorPayload
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&payload); err != nil || payload.Version != 1 || payload.Watermark == "" || payload.AfterAt == "" || payload.FilterHash == "" {
		return cursorPayload{}, ErrInvalidCursor
	}
	return payload, nil
}

func validStatus(value string) bool {
	return value == "" || value == "active" || value == "merged" || value == "closed"
}

func validActivation(value string) bool {
	return value == "" || value == "active" || value == "conflict" || value == "stale"
}
