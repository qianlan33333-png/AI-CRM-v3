package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/qianlan33333-png/AI-CRM-v3/internal/access/credential"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	accessport "github.com/qianlan33333-png/AI-CRM-v3/internal/access/port"
	platformaudit "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/audit"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/platform/idempotency"
)

var ErrWeComStaffProjectionUnavailable = errors.New("wecom staff projection unavailable")

type WeComStaffProjector struct {
	repository   accessport.Repository
	passwordHash string
	audit        interface {
		Append(context.Context, platformaudit.Event) (platformaudit.Event, error)
	}
}

func NewWeComStaffProjector(repository accessport.Repository, passwords Passwords, audit interface {
	Append(context.Context, platformaudit.Event) (platformaudit.Event, error)
}) (*WeComStaffProjector, error) {
	if repository == nil || passwords == nil || audit == nil {
		return nil, ErrWeComStaffProjectionUnavailable
	}
	// The random credential is intentionally discarded after hashing. Newly
	// projected staff can use provider-verified WeCom OAuth, but cannot know a
	// local password unless a super administrator explicitly resets it later.
	randomPassword, _, err := credential.IssueOpaque("staff_")
	if err != nil {
		return nil, err
	}
	passwordHash, err := passwords.Hash(randomPassword)
	if err != nil {
		return nil, err
	}
	return &WeComStaffProjector{repository: repository, passwordHash: passwordHash, audit: audit}, nil
}

func (service *WeComStaffProjector) ProjectWeComStaffWithin(ctx context.Context, runKey string, input []accessport.WeComStaffProjection, now time.Time) (accessport.WeComStaffProjectionResult, error) {
	if service == nil || service.repository == nil || service.audit == nil || !validStaffRunKey(runKey) || len(input) > 10000 {
		return accessport.WeComStaffProjectionResult{}, ErrWeComStaffProjectionUnavailable
	}
	canonical := make([]accessport.WeComStaffProjection, 0, len(input))
	seen := make(map[string]struct{}, len(input))
	for _, item := range input {
		providerID, err := domain.NormalizeWeComUserID(item.WeComUserID)
		if err != nil {
			return accessport.WeComStaffProjectionResult{}, domain.ErrInvalidInput
		}
		if _, exists := seen[providerID]; exists {
			continue
		}
		seen[providerID] = struct{}{}
		displayName := strings.TrimSpace(item.DisplayName)
		if displayName == "" {
			displayName = "企微客服 " + providerID
		}
		if len([]rune(displayName)) > 160 || strings.ContainsAny(displayName, "\x00\r\n") {
			return accessport.WeComStaffProjectionResult{}, domain.ErrInvalidInput
		}
		canonical = append(canonical, accessport.WeComStaffProjection{WeComUserID: providerID, DisplayName: displayName})
	}
	sort.Slice(canonical, func(i, j int) bool { return canonical[i].WeComUserID < canonical[j].WeComUserID })
	result := accessport.WeComStaffProjectionResult{Discovered: int64(len(canonical))}
	for _, item := range canonical {
		user, err := service.repository.UserByWeComUserID(ctx, item.WeComUserID, true)
		if err == nil {
			result.Existing++
			if !user.Active {
				result.Inactive++
			}
			continue
		}
		if !errors.Is(err, domain.ErrNotFound) {
			return accessport.WeComStaffProjectionResult{}, err
		}
		digest := sha256.Sum256([]byte(item.WeComUserID))
		user, err = service.repository.CreateUser(ctx, domain.User{
			Username: "wecom-staff-" + hex.EncodeToString(digest[:]), PasswordHash: service.passwordHash,
			DisplayName: item.DisplayName, WeComUserID: item.WeComUserID, Active: true,
			Roles: []domain.Role{domain.RoleViewer},
		})
		if err != nil {
			return accessport.WeComStaffProjectionResult{}, err
		}
		payload, _ := json.Marshal(map[string]any{"source": "wecom_follow_user_list", "roles": []domain.Role{domain.RoleViewer}})
		key, keyErr := idempotency.Parse("wecom-staff-project:" + runKey + ":" + hex.EncodeToString(digest[:12]))
		if keyErr != nil {
			return accessport.WeComStaffProjectionResult{}, keyErr
		}
		if _, err = service.audit.Append(ctx, platformaudit.Event{IdempotencyKey: key, Action: "access.wecom_staff_projected", ActorType: "system", ResourceType: "admin_user", ResourceID: strconv.FormatInt(user.ID, 10), Payload: payload, OccurredAt: now.UTC()}); err != nil {
			return accessport.WeComStaffProjectionResult{}, err
		}
		result.Created++
	}
	return result, nil
}

func validStaffRunKey(value string) bool {
	value = strings.TrimSpace(value)
	return value != "" && len(value) <= 120 && !strings.ContainsAny(value, "\x00\r\n")
}

var _ accessport.WeComStaffProjector = (*WeComStaffProjector)(nil)
