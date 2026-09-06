package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	openplatformport "github.com/qianlan33333-png/AI-CRM-v3/internal/openplatform/port"
)

// v1IdentityReference intentionally has no assurance field. A machine request
// can declare a reference only; trusted Identity adapters remain the sole
// source of verified assurance and V1 never provisions a customer.
type v1IdentityReference struct {
	Kind  string `json:"kind"`
	Scope string `json:"scope"`
	Value string `json:"value"`
}

type v1CustomerResolveInput struct {
	References []v1IdentityReference `json:"references"`
}

type v1CustomerInput struct {
	CustomerID int64 `json:"customer_id"`
}

func (executor *openPlatformExecutor) Available(_ context.Context, principal accessdomain.MachinePrincipal) ([]openplatformport.Descriptor, error) {
	if executor == nil {
		return nil, openplatformport.NewError(openplatformport.ErrorDependencyUnavailable, "open platform is not composed")
	}
	// Do not publish an operation merely because its route is known. Each false
	// value is an uncomposed Owner Port and therefore absent from the catalog.
	available := map[openplatformport.OperationID]bool{
		openplatformport.OperationCapabilitiesList: true,
		openplatformport.OperationCustomerResolve:  executor.identity != nil,
		openplatformport.OperationCustomerContext:  executor.profiles != nil,
		// Activities and AI are enabled only by their explicit V1 binders. The
		// legacy compatibility readers are deliberately not a substitute.
		openplatformport.OperationCustomerActivities: false,
		openplatformport.OperationAIReviewPlanCreate: false,
		openplatformport.OperationGet:                false,
	}
	return openplatformport.AvailableDescriptors(principal, available), nil
}

func (executor *openPlatformExecutor) Invoke(ctx context.Context, invocation openplatformport.Invocation) (openplatformport.Result, error) {
	if executor == nil {
		return openplatformport.Result{}, openplatformport.NewError(openplatformport.ErrorDependencyUnavailable, "open platform is not composed")
	}
	descriptor, known := openplatformport.DescriptorForOperation(invocation.Operation)
	if !known {
		return openplatformport.Result{}, openplatformport.NewError(openplatformport.ErrorNotFound, "operation not found")
	}
	available, err := executor.Available(ctx, invocation.Principal)
	if err != nil {
		return openplatformport.Result{}, err
	}
	composed := false
	for _, item := range available {
		if item.OperationID == invocation.Operation {
			composed = true
			break
		}
	}
	if !descriptor.Allows(invocation.Principal) {
		return openplatformport.Result{}, openplatformport.NewError(openplatformport.ErrorPermission, "operation is not granted")
	}
	if !composed {
		return openplatformport.Result{}, openplatformport.NewError(openplatformport.ErrorDependencyUnavailable, "operation is not composed")
	}
	switch invocation.Operation {
	case openplatformport.OperationCapabilitiesList:
		return executor.v1Capabilities(ctx, invocation.Principal)
	case openplatformport.OperationCustomerResolve:
		return executor.v1ResolveCustomer(ctx, invocation.Principal, invocation.Input)
	case openplatformport.OperationCustomerContext:
		return executor.v1CustomerContext(ctx, invocation.Principal, invocation.Input)
	default:
		return openplatformport.Result{}, openplatformport.NewError(openplatformport.ErrorDependencyUnavailable, "operation is not composed")
	}
}

func (executor *openPlatformExecutor) v1Capabilities(ctx context.Context, principal accessdomain.MachinePrincipal) (openplatformport.Result, error) {
	items, err := executor.Available(ctx, principal)
	if err != nil {
		return openplatformport.Result{}, err
	}
	return openplatformport.Result{Data: map[string]any{"schema_version": openplatformport.SchemaVersion, "operations": items}}, nil
}

func (executor *openPlatformExecutor) v1ResolveCustomer(ctx context.Context, principal accessdomain.MachinePrincipal, raw json.RawMessage) (openplatformport.Result, error) {
	var input v1CustomerResolveInput
	if err := decodeV1JSON(raw, &input); err != nil || len(input.References) == 0 || len(input.References) > 8 {
		return openplatformport.Result{}, openplatformport.NewError(openplatformport.ErrorValidation, "references are required")
	}
	references := make([]identitydomain.Reference, 0, len(input.References))
	seen := map[string]struct{}{}
	for _, inputReference := range input.References {
		kind := identitydomain.Kind(strings.TrimSpace(inputReference.Kind))
		// V1 makes scope explicit. It never uses a caller-supplied assurance,
		// and it does not retain the legacy implicit-scope convenience.
		if kind == "" || strings.TrimSpace(inputReference.Scope) == "" || strings.TrimSpace(inputReference.Value) == "" {
			return openplatformport.Result{}, openplatformport.NewError(openplatformport.ErrorValidation, "identity kind, scope and value are required")
		}
		reference, err := executor.trustedReference(kind, inputReference.Scope, inputReference.Value, "open_platform.v1")
		if err != nil {
			return openplatformport.Result{}, v1IdentityError(err)
		}
		key := string(reference.Kind) + "\x00" + reference.Scope + "\x00" + reference.Value
		if _, duplicate := seen[key]; duplicate {
			return openplatformport.Result{}, openplatformport.NewError(openplatformport.ErrorValidation, "duplicate identity reference")
		}
		seen[key] = struct{}{}
		references = append(references, reference)
	}
	resolved, err := executor.resolveReferences(ctx, references)
	if err != nil {
		return openplatformport.Result{}, v1IdentityError(err)
	}
	if err := executor.ensureCustomerScope(ctx, principal, resolved.CustomerID, references); err != nil {
		return openplatformport.Result{}, openplatformport.NewError(openplatformport.ErrorNotFound, "customer is outside the granted scope")
	}
	return openplatformport.Result{Data: map[string]any{
		"customer_id": resolved.CustomerID,
		"identity_id": resolved.IdentityID,
		"status":      string(resolved.Status),
	}}, nil
}

func (executor *openPlatformExecutor) v1CustomerContext(ctx context.Context, principal accessdomain.MachinePrincipal, raw json.RawMessage) (openplatformport.Result, error) {
	var input v1CustomerInput
	if err := decodeV1JSON(raw, &input); err != nil || input.CustomerID < 1 {
		return openplatformport.Result{}, openplatformport.NewError(openplatformport.ErrorValidation, "customer_id is required")
	}
	customerID := customerdomain.CustomerID(input.CustomerID)
	if err := executor.ensureCustomerScope(ctx, principal, customerID, nil); err != nil {
		return openplatformport.Result{}, openplatformport.NewError(openplatformport.ErrorNotFound, "customer is outside the granted scope")
	}
	profile, err := executor.profiles.ReadSidebarProfile(ctx, customerID)
	if err != nil {
		return openplatformport.Result{}, openplatformport.NewError(openplatformport.ErrorDependencyUnavailable, "customer context is unavailable")
	}
	if profile.CustomerID != customerID {
		return openplatformport.Result{}, openplatformport.NewError(openplatformport.ErrorNotFound, "customer not found")
	}
	return openplatformport.Result{Data: profile}, nil
}

func decodeV1JSON(raw json.RawMessage, target any) error {
	if len(raw) == 0 {
		raw = []byte(`{}`)
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return fmt.Errorf("trailing JSON")
	}
	return nil
}

func v1IdentityError(err error) error {
	switch {
	case errors.Is(err, errOpenPlatformIdentityNotFound):
		return openplatformport.NewError(openplatformport.ErrorNotFound, "identity was not found")
	case errors.Is(err, errOpenPlatformIdentityConflict):
		return openplatformport.NewError(openplatformport.ErrorIdentityConflict, "identity references conflict")
	case errors.Is(err, errOpenPlatformIdentityPending), errors.Is(err, errOpenPlatformIdentityScopeDenied):
		return openplatformport.NewError(openplatformport.ErrorIdentityPending, "identity is pending or scope is unavailable")
	default:
		return openplatformport.NewError(openplatformport.ErrorDependencyUnavailable, "identity resolver is unavailable")
	}
}

var _ openplatformport.OperationService = (*openPlatformExecutor)(nil)
var _ = identityport.ResolveFound
