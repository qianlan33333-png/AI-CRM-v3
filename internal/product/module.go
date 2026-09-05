package product

import (
	"context"
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"

	productapp "github.com/qianlan33333-png/AI-CRM-v3/internal/product/app"
	producthttp "github.com/qianlan33333-png/AI-CRM-v3/internal/product/http"
	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
)

// ModuleRegistration is the Product composition boundary. Product has no
// provider worker: external-push is a local definition/acceptance projection
// until a future outbound adapter is explicitly registered by its owner.
type ModuleRegistration struct{}

type HTTPBindings struct {
	Products http.Handler
}

func NewModuleRegistration() *ModuleRegistration { return &ModuleRegistration{} }

func (m *ModuleRegistration) Bind(catalog producthttp.CatalogApplication, lifecycle productport.LocalProductLifecycleApplication, service productport.ServicePeriodApplication, external productport.CommerceExternalPushApplication, security producthttp.RequestSecurity) (HTTPBindings, error) {
	if m == nil {
		return HTTPBindings{}, errors.New("product module is required")
	}
	handler, err := producthttp.NewHandler(catalog, lifecycle, service, external, security)
	if err != nil {
		return HTTPBindings{}, err
	}
	return HTTPBindings{Products: handler}, nil
}

func (m *ModuleRegistration) Readiness(ctx context.Context, pool *pgxpool.Pool) error {
	if m == nil || pool == nil {
		return errors.New("product module dependencies are required")
	}
	var ready bool
	err := pool.QueryRow(ctx, `SELECT NOT EXISTS (SELECT 1 FROM unnest(ARRAY['products','product_operation_receipts','product_external_push_configurations','product_external_push_tests','product_imported_service_period_definitions']) AS required(name) WHERE to_regclass(current_schema() || '.' || required.name) IS NULL)`).Scan(&ready)
	if err != nil {
		return err
	}
	if !ready {
		return errors.New("product schema is not ready")
	}
	return nil
}

var _ producthttp.CatalogApplication = (*productapp.Service)(nil)
var _ productport.ProductOptionReader = (*productapp.Service)(nil)
