package media

import (
	"context"
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
	mediaapp "github.com/qianlan33333-png/AI-CRM-v3/internal/media/app"
	mediahttp "github.com/qianlan33333-png/AI-CRM-v3/internal/media/http"
	mediaport "github.com/qianlan33333-png/AI-CRM-v3/internal/media/port"
)

// ModuleRegistration is Media's stable composition contract. The module owns
// HTTP bindings and readiness only; it has no process role or provider worker.
type ModuleRegistration struct{}
type HTTPBindings struct{ Media http.Handler }

// ContentDeliveryBindings are intentionally transport-free. A future Group
// Ops composition root can receive the ContentDelivery service and the source
// capturer through Media's stable port without importing Media Store.
type ContentDeliveryBindings struct {
	ContentDelivery mediaport.ContentDeliveryService
	SourceCapturer  mediaport.GroupOpsMaterialSourceCapturer
}

func NewModuleRegistration() *ModuleRegistration { return &ModuleRegistration{} }
func (m *ModuleRegistration) Bind(service mediaapp.HTTPFacade, security mediahttp.RequestSecurity) (HTTPBindings, error) {
	if m == nil {
		return HTTPBindings{}, errors.New("media module is required")
	}
	handler, err := mediahttp.NewHandler(service, security)
	if err != nil {
		return HTTPBindings{}, err
	}
	return HTTPBindings{Media: handler}, nil
}
func (m *ModuleRegistration) BindContentDelivery(service mediaport.ContentDeliveryService, capturer mediaport.GroupOpsMaterialSourceCapturer) (ContentDeliveryBindings, error) {
	if m == nil || service == nil || capturer == nil {
		return ContentDeliveryBindings{}, errors.New("media content delivery dependencies are required")
	}
	return ContentDeliveryBindings{ContentDelivery: service, SourceCapturer: capturer}, nil
}
func (m *ModuleRegistration) Readiness(ctx context.Context, pool *pgxpool.Pool) error {
	if m == nil || pool == nil {
		return errors.New("media module dependencies are required")
	}
	var ready bool
	err := pool.QueryRow(ctx, `SELECT NOT EXISTS (SELECT 1 FROM unnest(ARRAY['media_blobs','media_images','media_attachments','media_miniprograms','media_group_invites','media_operation_receipts','media_audit_events','media_outbox','media_attachment_uploads','media_attachment_upload_parts','media_content_packages','media_content_package_versions','media_content_package_version_refs','media_content_delivery_receipts','media_content_delivery_bindings']) AS required(name) WHERE to_regclass(current_schema() || '.' || required.name) IS NULL)`).Scan(&ready)
	if err != nil {
		return err
	}
	if !ready {
		return errors.New("media schema is not ready")
	}
	return nil
}
