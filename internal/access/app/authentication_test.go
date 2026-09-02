package app

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
)

type testUOW struct{}

func (testUOW) Within(ctx context.Context, callback func(context.Context) error) error {
	return callback(ctx)
}

type testPasswords struct{}

func (testPasswords) Hash(value string) (string, error) {
	if len(value) < 3 {
		return "", domain.ErrInvalidInput
	}
	return "hash:" + value, nil
}
func (testPasswords) Verify(value, encoded string) bool { return encoded == "hash:"+value }

type memoryRepository struct {
	mu       sync.Mutex
	users    map[int64]domain.User
	sessions map[[32]byte]domain.Session
	limits   map[[32]byte]domain.LoginRateLimit
	audits   []domain.LoginAudit
	nextID   int64
}

func newMemoryRepository() *memoryRepository {
	return &memoryRepository{users: map[int64]domain.User{}, sessions: map[[32]byte]domain.Session{}, limits: map[[32]byte]domain.LoginRateLimit{}, nextID: 1}
}

func (repo *memoryRepository) CountUsers(context.Context) (int64, error) {
	return int64(len(repo.users)), nil
}
func (repo *memoryRepository) UserByID(_ context.Context, id int64, _ bool) (domain.User, error) {
	user, ok := repo.users[id]
	if !ok {
		return domain.User{}, domain.ErrNotFound
	}
	return user, nil
}
func (repo *memoryRepository) UserByUsername(_ context.Context, username string, _ bool) (domain.User, error) {
	for _, user := range repo.users {
		if user.Username == username {
			return user, nil
		}
	}
	return domain.User{}, domain.ErrNotFound
}
func (repo *memoryRepository) CreateUser(_ context.Context, user domain.User) (domain.User, error) {
	user.ID = repo.nextID
	repo.nextID++
	user.SessionVersion = 1
	repo.users[user.ID] = user
	return user, nil
}
func (repo *memoryRepository) BootstrapUser(ctx context.Context, user domain.User) (domain.User, bool, error) {
	if len(repo.users) > 0 {
		existing, err := repo.UserByUsername(ctx, user.Username, false)
		return existing, false, err
	}
	created, err := repo.CreateUser(ctx, user)
	return created, true, err
}
func (repo *memoryRepository) SetActive(_ context.Context, id int64, active bool, _ time.Time) error {
	user, ok := repo.users[id]
	if !ok {
		return domain.ErrNotFound
	}
	user.Active, user.SessionVersion = active, user.SessionVersion+1
	repo.users[id] = user
	return nil
}
func (repo *memoryRepository) SetWeComUserID(_ context.Context, id int64, value string, _ time.Time) error {
	user, ok := repo.users[id]
	if !ok {
		return domain.ErrNotFound
	}
	user.WeComUserID, user.SessionVersion = value, user.SessionVersion+1
	repo.users[id] = user
	return nil
}
func (repo *memoryRepository) ReplaceRoles(_ context.Context, id int64, roles []domain.Role, _ time.Time) error {
	user, ok := repo.users[id]
	if !ok {
		return domain.ErrNotFound
	}
	user.Roles, user.SessionVersion = roles, user.SessionVersion+1
	repo.users[id] = user
	return nil
}
func (repo *memoryRepository) SetPasswordHash(_ context.Context, id int64, value string, _ time.Time) error {
	user, ok := repo.users[id]
	if !ok {
		return domain.ErrNotFound
	}
	user.PasswordHash, user.SessionVersion = value, user.SessionVersion+1
	repo.users[id] = user
	return nil
}
func (repo *memoryRepository) SetLastLogin(_ context.Context, id int64, now time.Time) error {
	user, ok := repo.users[id]
	if !ok {
		return domain.ErrNotFound
	}
	user.LastLoginAt = &now
	repo.users[id] = user
	return nil
}
func (repo *memoryRepository) CreateSession(_ context.Context, session domain.Session) (domain.Session, error) {
	session.ID = int64(len(repo.sessions) + 1)
	repo.sessions[session.TokenDigest] = session
	return session, nil
}
func (repo *memoryRepository) SessionByTokenDigest(_ context.Context, digest [32]byte, _ bool) (domain.Session, error) {
	session, ok := repo.sessions[digest]
	if !ok {
		return domain.Session{}, domain.ErrNotFound
	}
	session.User = repo.users[session.AdminUserID]
	return session, nil
}
func (repo *memoryRepository) TouchSession(context.Context, int64, time.Time) error { return nil }
func (repo *memoryRepository) RevokeSession(_ context.Context, digest [32]byte, reason string, now time.Time) (bool, error) {
	session, ok := repo.sessions[digest]
	if !ok {
		return false, nil
	}
	session.RevokedAt, session.RevokedReason = &now, reason
	repo.sessions[digest] = session
	return true, nil
}
func (repo *memoryRepository) LoginRateLimit(_ context.Context, digest [32]byte, _ bool) (domain.LoginRateLimit, error) {
	limit, ok := repo.limits[digest]
	if !ok {
		limit = domain.LoginRateLimit{KeyDigest: digest, WindowStartedAt: testNow, UpdatedAt: testNow}
		repo.limits[digest] = limit
	}
	return limit, nil
}
func (repo *memoryRepository) SaveLoginRateLimit(_ context.Context, limit domain.LoginRateLimit) error {
	repo.limits[limit.KeyDigest] = limit
	return nil
}
func (repo *memoryRepository) AppendLoginAudit(_ context.Context, audit domain.LoginAudit) error {
	repo.audits = append(repo.audits, audit)
	return nil
}
func (*memoryRepository) AppendAccessAudit(context.Context, domain.AccessAudit) error { return nil }

var testNow = time.Date(2026, 9, 2, 8, 0, 0, 0, time.UTC)

func authenticationFixture(t *testing.T, maxFailures int) (*Authentication, *memoryRepository, *time.Time) {
	t.Helper()
	repository := newMemoryRepository()
	repository.users[1] = domain.User{ID: 1, Username: "operator", PasswordHash: "hash:correct-password", Active: true,
		SessionVersion: 1, Roles: []domain.Role{domain.RoleSuperAdmin}}
	now := testNow
	service, err := NewAuthentication(repository, testUOW{}, testPasswords{}, AuthenticationConfig{
		SessionTTL: time.Hour, Window: time.Minute, MaxFailures: maxFailures, BlockFor: time.Minute,
		Now: func() time.Time { return now }, DummyPHCHash: "hash:dummy-password",
	})
	if err != nil {
		t.Fatal(err)
	}
	return service, repository, &now
}

func TestSessionReplayAfterLogoutAndCSRFValidation(t *testing.T) {
	service, _, _ := authenticationFixture(t, 5)
	issued, err := service.Login(context.Background(), LoginCommand{Username: "operator", Password: "correct-password", Remote: "127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.AuthorizeCSRF(context.Background(), issued.SessionToken, issued.CSRFToken, "replayed-wrong-token"); !errors.Is(err, domain.ErrCSRFRequired) {
		t.Fatalf("wrong CSRF error = %v", err)
	}
	if err = service.Logout(context.Background(), issued.SessionToken, issued.CSRFToken, issued.CSRFToken); err != nil {
		t.Fatal(err)
	}
	if _, err = service.Authenticate(context.Background(), issued.SessionToken); !errors.Is(err, domain.ErrAuthentication) {
		t.Fatalf("replayed session error = %v", err)
	}
}

func TestExpiredSessionRejected(t *testing.T) {
	service, _, now := authenticationFixture(t, 5)
	issued, err := service.Login(context.Background(), LoginCommand{Username: "operator", Password: "correct-password", Remote: "127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	*now = now.Add(time.Hour)
	if _, err = service.Authenticate(context.Background(), issued.SessionToken); !errors.Is(err, domain.ErrAuthentication) {
		t.Fatalf("expired session error = %v", err)
	}
}

func TestPersistentLoginRateLimit(t *testing.T) {
	service, repository, _ := authenticationFixture(t, 2)
	command := LoginCommand{Username: "operator", Password: "wrong-password", Remote: "127.0.0.1"}
	for index := 0; index < 2; index++ {
		if _, err := service.Login(context.Background(), command); !errors.Is(err, domain.ErrInvalidCredentials) {
			t.Fatalf("attempt %d error = %v", index+1, err)
		}
	}
	if _, err := service.Login(context.Background(), command); !errors.Is(err, domain.ErrRateLimited) {
		t.Fatalf("rate limited error = %v", err)
	}
	if len(repository.audits) != 3 || repository.audits[2].Outcome != "rate_limited" {
		t.Fatalf("audit outcomes = %#v", repository.audits)
	}
}

func TestSessionVersionInvalidatesExistingSession(t *testing.T) {
	service, repository, _ := authenticationFixture(t, 5)
	issued, err := service.Login(context.Background(), LoginCommand{Username: "operator", Password: "correct-password", Remote: "127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	user := repository.users[1]
	user.SessionVersion++
	repository.users[1] = user
	if _, err = service.Authenticate(context.Background(), issued.SessionToken); !errors.Is(err, domain.ErrAuthentication) {
		t.Fatalf("version mismatch error = %v", err)
	}
}

func TestAdminCannotEscalatePrivileges(t *testing.T) {
	service, err := NewManagement(newMemoryRepository(), testUOW{}, testPasswords{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	actor := domain.Principal{Kind: domain.KindAdmin, InternalID: 2, Roles: []domain.Role{domain.RoleAdmin}}
	_, err = service.AddUser(context.Background(), actor, AddUserInput{
		Username: "new-user", Password: "a-valid-password", DisplayName: "New", Roles: []domain.Role{domain.RoleSuperAdmin},
	})
	if !errors.Is(err, domain.ErrPermissionDenied) {
		t.Fatalf("privilege escalation error = %v", err)
	}
}

func TestBootstrapIsIdempotentAndDoesNotRotateExistingPassword(t *testing.T) {
	repository := newMemoryRepository()
	service, err := NewManagement(repository, testUOW{}, testPasswords{}, func() time.Time { return testNow })
	if err != nil {
		t.Fatal(err)
	}
	input := BootstrapInput{Username: "first-admin", Password: "initial-password", DisplayName: "First Admin"}
	first, created, err := service.Bootstrap(context.Background(), input)
	if err != nil || !created {
		t.Fatalf("first bootstrap created=%v err=%v", created, err)
	}
	again, created, err := service.Bootstrap(context.Background(), BootstrapInput{
		Username: "first-admin", Password: "different-password", DisplayName: "Changed",
	})
	if err != nil || created || again.ID != first.ID || again.PasswordHash != "hash:initial-password" {
		t.Fatalf("second bootstrap=%#v created=%v err=%v", again, created, err)
	}
}
