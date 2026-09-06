package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	openplatformport "github.com/qianlan33333-png/AI-CRM-v3/internal/openplatform/port"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
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
		openplatformport.OperationCustomerActivities: executor.activities != nil,
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
	if !descriptor.Allows(invocation.Principal) {
		return executor.v1AuditedError(ctx, invocation, openplatformport.NewError(openplatformport.ErrorPermission, "operation is not granted"))
	}
	available, err := executor.Available(ctx, invocation.Principal)
	if err != nil {
		return executor.v1AuditedError(ctx, invocation, err)
	}
	composed := false
	for _, item := range available {
		if item.OperationID == invocation.Operation {
			composed = true
			break
		}
	}
	if !composed {
		return executor.v1AuditedError(ctx, invocation, openplatformport.NewError(openplatformport.ErrorDependencyUnavailable, "operation is not composed"))
	}
	var result openplatformport.Result
	switch invocation.Operation {
	case openplatformport.OperationCapabilitiesList:
		result, err = executor.v1Capabilities(ctx, invocation.Principal)
	case openplatformport.OperationCustomerResolve:
		result, err = executor.v1ResolveCustomer(ctx, invocation.Principal, invocation.Input)
	case openplatformport.OperationCustomerContext:
		result, err = executor.v1CustomerContext(ctx, invocation.Principal, invocation.Input)
	case openplatformport.OperationCustomerActivities:
		result, err = executor.v1CustomerActivities(ctx, invocation.Principal, invocation.Input)
	default:
		err = openplatformport.NewError(openplatformport.ErrorDependencyUnavailable, "operation is not composed")
	}
	if err != nil {
		return executor.v1AuditedError(ctx, invocation, err)
	}
	if auditErr := executor.recordV1Operation(ctx, invocation, "succeeded"); auditErr != nil {
		return openplatformport.Result{}, openplatformport.NewError(openplatformport.ErrorDependencyUnavailable, "operation audit is unavailable")
	}
	return result, nil
}

func (executor *openPlatformExecutor) v1AuditedError(ctx context.Context, invocation openplatformport.Invocation, operationErr error) (openplatformport.Result, error) {
	outcome := string(openplatformport.ErrorCodeOf(operationErr))
	if auditErr := executor.recordV1Operation(ctx, invocation, outcome); auditErr != nil {
		return openplatformport.Result{}, openplatformport.NewError(openplatformport.ErrorDependencyUnavailable, "operation audit is unavailable")
	}
	return openplatformport.Result{}, operationErr
}

func (executor *openPlatformExecutor) recordV1Operation(ctx context.Context, invocation openplatformport.Invocation, outcome string) error {
	if executor == nil || invocation.Principal.ClientRecord < 1 {
		return nil
	}
	if executor.operationAudit == nil {
		return errors.New("operation auditor is not composed")
	}
	return executor.operationAudit.Record(ctx, invocation.Principal, invocation.Operation, invocation.RequestID, outcome)
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
		return openplatformport.Result{}, v1CustomerScopeError(err)
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
		return openplatformport.Result{}, v1CustomerScopeError(err)
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
	if !openplatformport.ValidJSONObject(raw) {
		return fmt.Errorf("invalid JSON object")
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

func v1CustomerScopeError(err error) error {
	if errors.Is(err, errOpenPlatformOwnerUnavailable) {
		return openplatformport.NewError(openplatformport.ErrorDependencyUnavailable, "customer ownership projection is unavailable")
	}
	return openplatformport.NewError(openplatformport.ErrorNotFound, "customer is outside the granted scope")
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

type openPlatformOperationAuditor struct {
	writer openPlatformMachineAuditWriter
	uow    platformport.UnitOfWork
}

// Record stores only the operation name and terminal category. Request bodies,
// cursors, bearer tokens, identity values, and user-supplied request IDs are
// never retained in this audit fact.
func (auditor *openPlatformOperationAuditor) Record(ctx context.Context, principal accessdomain.MachinePrincipal, operation openplatformport.OperationID, requestID, outcome string) error {
	if principal.ClientRecord < 1 {
		return nil
	}
	if auditor == nil || auditor.writer == nil || auditor.uow == nil {
		return errors.New("operation auditor is not composed")
	}
	requestDigest := sha256.Sum256([]byte(requestID))
	payload, _ := json.Marshal(map[string]string{"operation": string(operation), "request_id_digest": hex.EncodeToString(requestDigest[:])})
	return auditor.uow.Within(ctx, func(tx context.Context) error {
		return auditor.writer.AppendMachineAudit(tx, accessdomain.MachineAudit{MachineClientID: principal.ClientRecord, Action: "open_platform_operation", Outcome: outcome, Details: payload, CreatedAt: time.Now().UTC()})
	})
}
