package media

import (
	"context"
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
	mediahttp "github.com/qianlan33333-png/AI-CRM-v3/internal/media/http"
	mediastore "github.com/qianlan33333-png/AI-CRM-v3/internal/media/store"
)

// ModuleRegistration is Media's stable composition contract. The module owns
// HTTP bindings and readiness only; it has no process role or provider worker.
type ModuleRegistration struct{}
type HTTPBindings struct{ Media http.Handler }

func NewModuleRegistration() *ModuleRegistration { return &ModuleRegistration{} }
func (m *ModuleRegistration) Bind(repository *mediastore.Repository, security mediahttp.RequestSecurity) (HTTPBindings, error) {
	if m == nil {
		return HTTPBindings{}, errors.New("media module is required")
	}
	handler, err := mediahttp.NewHandler(repository, security)
	if err != nil {
		return HTTPBindings{}, err
	}
	return HTTPBindings{Media: handler}, nil
}
func (m *ModuleRegistration) Readiness(ctx context.Context, pool *pgxpool.Pool) error {
	if m == nil || pool == nil {
		return errors.New("media module dependencies are required")
	}
	var ready bool
	err := pool.QueryRow(ctx, `SELECT NOT EXISTS (SELECT 1 FROM unnest(ARRAY['media_blobs','media_images','media_attachments','media_miniprograms','media_group_invites','media_operation_receipts','media_audit_events','media_outbox','media_attachment_uploads','media_attachment_upload_parts']) AS required(name) WHERE to_regclass(current_schema() || '.' || required.name) IS NULL)`).Scan(&ready)
	if err != nil {
		return err
	}
	if !ready {
		return errors.New("media schema is not ready")
	}
	return nil
}
