// Package excel connects the existing AI Assistant to the independent
// preparation/observation component. It never calls a Provider writer.
package excel

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	ai "github.com/qianlan33333-png/AI-CRM-v3/internal/aiassistant/port"
	effect "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	outbound "github.com/qianlan33333-png/AI-CRM-v3/internal/outbound/port"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

var ErrUnavailable = errors.New("excel component unavailable")

type InputError struct{ Message string }

func (e *InputError) Error() string { return "invalid excel input" }

type Client struct {
	Base, Token string
	HTTP        *http.Client
}

func NewClient(base, token string) (*Client, error) {
	if base == "" && token == "" {
		return nil, nil
	}
	u, err := url.Parse(base)
	if err != nil || u.Scheme != "http" || (u.Hostname() != "127.0.0.1" && u.Hostname() != "localhost") || u.User != nil || u.RawQuery != "" || u.Path != "" || len(token) < 32 {
		return nil, ErrUnavailable
	}
	return &Client{Base: base, Token: token, HTTP: &http.Client{Timeout: 2 * time.Minute, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}
func (c *Client) Call(ctx context.Context, method, path, key string, body []byte, out any) error {
	if c == nil {
		return ErrUnavailable
	}
	r, err := http.NewRequestWithContext(ctx, method, c.Base+path, bytes.NewReader(body))
	if err != nil {
		return ErrUnavailable
	}
	r.Header.Set("Authorization", "Bearer "+c.Token)
	r.Header.Set("Content-Type", "application/json")
	if key != "" {
		r.Header.Set("Idempotency-Key", key)
	}
	response, err := c.HTTP.Do(r)
	if err != nil {
		return ErrUnavailable
	}
	defer response.Body.Close()
	if response.StatusCode == 400 {
		var input struct {
			Message string `json:"message"`
		}
		_ = json.NewDecoder(io.LimitReader(response.Body, 4096)).Decode(&input)
		return &InputError{Message: input.Message}
	}
	if response.StatusCode != 200 {
		return ErrUnavailable
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, 32<<20))
	if err != nil {
		return ErrUnavailable
	}
	if out == nil {
		return nil
	}
	if b, ok := out.(*[]byte); ok {
		*b = raw
		return nil
	}
	if json.Unmarshal(raw, out) != nil {
		return ErrUnavailable
	}
	return nil
}
func (c *Client) JSON(ctx context.Context, path string, input, out any) error {
	raw, err := json.Marshal(input)
	if err != nil {
		return err
	}
	return c.Call(ctx, http.MethodPost, path, "", raw, out)
}
func (c *Client) LoadExcelCard(ctx context.Context, card ai.ExcelCard) (outbound.PrivateMessageAttachment, error) {
	var raw []byte
	if strings.TrimSpace(card.Title) == "" {
		return outbound.PrivateMessageAttachment{}, outbound.PayloadPreparationError("title_missing")
	}
	if card.CoverDigest == "" {
		return outbound.PrivateMessageAttachment{}, outbound.PayloadPreparationError("cover_missing")
	}
	if !card.Valid() {
		return outbound.PrivateMessageAttachment{}, ErrUnavailable
	}
	if err := c.Call(ctx, http.MethodGet, "/covers/"+url.PathEscape(string(card.CoverDigest)), "", nil, &raw); err != nil {
		return outbound.PrivateMessageAttachment{}, err
	}
	hash := sha256.Sum256(raw)
	if effect.Digest("sha256:"+hex.EncodeToString(hash[:])) != card.CoverDigest || len(raw) > 2<<20 {
		return outbound.PrivateMessageAttachment{}, ErrUnavailable
	}
	mime, name := "image/png", "cover.png"
	if len(raw) > 3 && bytes.Equal(raw[:3], []byte{255, 216, 255}) {
		mime, name = "image/jpeg", "cover.jpg"
	} else if !bytes.HasPrefix(raw, []byte{137, 80, 78, 71, 13, 10, 26, 10}) {
		return outbound.PrivateMessageAttachment{}, ErrUnavailable
	}
	return outbound.PrivateMessageAttachment{Kind: "mini_program", Content: raw, MediaType: mime, FileName: name, AppID: card.AppID, PagePath: card.Path, Title: card.Title}, nil
}
func snapshotKey(id ai.PlanID, version int64) string {
	return strings.Join([]string{fmtInt(int64(id)), fmtInt(version)}, ":")
}
