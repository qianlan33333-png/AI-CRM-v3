package port

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"sort"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
)

// SchemaVersion identifies the stable V1 Operation Catalog contract. REST and
// MCP expose the same descriptors and invoke the same application service.
const SchemaVersion = "v1"

type OperationID string

const (
	OperationCapabilitiesList   OperationID = "platform.capabilities.list"
	OperationCustomerResolve    OperationID = "customer.resolve"
	OperationCustomerContext    OperationID = "customer.context.get"
	OperationCustomerActivities OperationID = "customer.activities.list"
	OperationAIReviewPlanCreate OperationID = "ai.review_plan.create"
	OperationGet                OperationID = "operation.get"
)

type Capability string

const (
	CapabilityPlatformCapabilitiesRead Capability = "platform.capabilities.read"
	CapabilityCustomerResolve          Capability = "customer.resolve"
	CapabilityCustomerRead             Capability = "customer.read"
	CapabilityCustomerActivityRead     Capability = "customer.activity.read"
	CapabilityAIReviewPlanCreate       Capability = "ai.review_plan.create"
	CapabilityOperationRead            Capability = "operation.read"
)

// Descriptor is the single catalog entry used by REST, MCP, administration,
// and contract tests. Availability is evaluated by the composed application;
// no transport independently invents a route policy.
type Descriptor struct {
	OperationID   OperationID `json:"operation_id"`
	RESTMethod    string      `json:"rest_method"`
	RESTPath      string      `json:"rest_path"`
	MCPTool       string      `json:"mcp_tool"`
	Capability    Capability  `json:"capability"`
	RequiredScope string      `json:"required_scope"`
	SchemaVersion string      `json:"schema_version"`
	ActivityTypes []string    `json:"activity_types,omitempty"`
}

// OperationCatalog is deliberately data, rather than path-prefix dispatch, so
// an endpoint cannot accidentally become available to a machine client.
func OperationCatalog() []Descriptor {
	return []Descriptor{
		{OperationID: OperationCapabilitiesList, RESTMethod: "GET", RESTPath: "/open/v1/capabilities", MCPTool: "list_capabilities", Capability: CapabilityPlatformCapabilitiesRead, RequiredScope: "read", SchemaVersion: SchemaVersion},
		{OperationID: OperationCustomerResolve, RESTMethod: "POST", RESTPath: "/open/v1/customers:resolve", MCPTool: "resolve_customer", Capability: CapabilityCustomerResolve, RequiredScope: "read", SchemaVersion: SchemaVersion},
		{OperationID: OperationCustomerContext, RESTMethod: "GET", RESTPath: "/open/v1/customers/{customer_id}", MCPTool: "get_customer_context", Capability: CapabilityCustomerRead, RequiredScope: "read", SchemaVersion: SchemaVersion},
		{OperationID: OperationCustomerActivities, RESTMethod: "GET", RESTPath: "/open/v1/customers/{customer_id}/activities", MCPTool: "list_customer_activities", Capability: CapabilityCustomerActivityRead, RequiredScope: "read", SchemaVersion: SchemaVersion, ActivityTypes: []string{"message", "survey", "radar", "order"}},
		{OperationID: OperationAIReviewPlanCreate, RESTMethod: "POST", RESTPath: "/open/v1/ai/review-plans", MCPTool: "create_ai_review_plan", Capability: CapabilityAIReviewPlanCreate, RequiredScope: "write", SchemaVersion: SchemaVersion},
		{OperationID: OperationGet, RESTMethod: "GET", RESTPath: "/open/v1/operations/{operation_id}", MCPTool: "get_operation_status", Capability: CapabilityOperationRead, RequiredScope: "read", SchemaVersion: SchemaVersion},
	}
}

func DescriptorForOperation(operation OperationID) (Descriptor, bool) {
	for _, descriptor := range OperationCatalog() {
		if descriptor.OperationID == operation {
			return descriptor, true
		}
	}
	return Descriptor{}, false
}

func DescriptorForMCPTool(tool string) (Descriptor, bool) {
	for _, descriptor := range OperationCatalog() {
		if descriptor.MCPTool == tool {
			return descriptor, true
		}
	}
	return Descriptor{}, false
}

// Allows requires both the narrowed token scope and the current client grant.
// A token minted before a capability change remains safe because Access reloads
// the client and the application checks the effective principal every call.
func (descriptor Descriptor) Allows(principal accessdomain.MachinePrincipal) bool {
	return principal.HasScope(descriptor.RequiredScope) && principal.HasCapability(string(descriptor.Capability))
}

func AvailableDescriptors(principal accessdomain.MachinePrincipal, available map[OperationID]bool) []Descriptor {
	result := make([]Descriptor, 0, len(available))
	for _, descriptor := range OperationCatalog() {
		if available[descriptor.OperationID] && descriptor.Allows(principal) {
			copy := descriptor
			copy.ActivityTypes = append([]string(nil), descriptor.ActivityTypes...)
			result = append(result, copy)
		}
	}
	sort.Slice(result, func(left, right int) bool { return result[left].OperationID < result[right].OperationID })
	return result
}

// ActivityCursorContract freezes the opaque-cursor safeguards for the aggregate
// activity stream. The application signs this binding and keeps one position
// per Owner Port; it must reject a cursor when the customer, selected types or
// effective grant differ, and must never advance it after an Owner failure.
type ActivityCursorContract struct {
	BindsCustomer      bool
	BindsTypes         bool
	BindsGrant         bool
	PerTypeCursor      bool
	NoAdvanceOnFailure bool
}

func CustomerActivityCursorContract() ActivityCursorContract {
	return ActivityCursorContract{BindsCustomer: true, BindsTypes: true, BindsGrant: true, PerTypeCursor: true, NoAdvanceOnFailure: true}
}

// Invocation is transport-normalized. REST path/query values must be encoded
// into Input using the same V1 DTO fields accepted by the corresponding MCP
// tool, so the application sees one semantic request shape.
type Invocation struct {
	Operation      OperationID
	Principal      accessdomain.MachinePrincipal
	RequestID      string
	IdempotencyKey string
	Path           map[string]string
	Input          json.RawMessage
}

type Result struct {
	Data any
}

type ErrorCode string

const (
	ErrorAuthentication        ErrorCode = "authentication"
	ErrorPermission            ErrorCode = "permission"
	ErrorValidation            ErrorCode = "validation"
	ErrorNotFound              ErrorCode = "not_found"
	ErrorIdentityPending       ErrorCode = "identity_pending"
	ErrorIdentityConflict      ErrorCode = "identity_conflict"
	ErrorRateLimited           ErrorCode = "rate_limited"
	ErrorDependencyUnavailable ErrorCode = "dependency_unavailable"
	ErrorOutcomeUnknown        ErrorCode = "outcome_unknown"
	ErrorConflict              ErrorCode = "conflict"
)

type OperationError struct {
	Code    ErrorCode
	Message string
}

func (e *OperationError) Error() string {
	if e == nil {
		return ""
	}
	if e.Message != "" {
		return e.Message
	}
	return string(e.Code)
}

func NewError(code ErrorCode, message string) error {
	return &OperationError{Code: code, Message: message}
}

func ErrorCodeOf(err error) ErrorCode {
	var operationError *OperationError
	if errors.As(err, &operationError) && operationError != nil {
		return operationError.Code
	}
	return ErrorDependencyUnavailable
}

// OperationService is the V1 application boundary. HTTP and MCP must invoke
// this exact service; it owns capability, scope, identity, idempotency and
// owner-Port semantics rather than leaving those decisions to either transport.
type OperationService interface {
	Available(context.Context, accessdomain.MachinePrincipal) ([]Descriptor, error)
	Invoke(context.Context, Invocation) (Result, error)
}

// ValidJSONObject accepts exactly one JSON object and rejects duplicate member
// names at every nesting level. The same check is used before REST and MCP
// hand an input to an operation, so a later duplicate cannot produce a
// different semantic request or idempotency digest by transport.
func ValidJSONObject(raw []byte) bool {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := scanJSONValue(decoder, true); err != nil {
		return false
	}
	var trailing any
	return errors.Is(decoder.Decode(&trailing), io.EOF)
}

func scanJSONValue(decoder *json.Decoder, requireObject bool) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, isDelimiter := token.(json.Delim)
	if !isDelimiter {
		if requireObject {
			return errors.New("JSON input must be an object")
		}
		return nil
	}
	switch delimiter {
	case '{':
		seen := map[string]struct{}{}
		for decoder.More() {
			key, err := decoder.Token()
			if err != nil {
				return err
			}
			name, ok := key.(string)
			if !ok {
				return errors.New("JSON object key is invalid")
			}
			if _, duplicate := seen[name]; duplicate {
				return errors.New("duplicate JSON member")
			}
			seen[name] = struct{}{}
			if err = scanJSONValue(decoder, false); err != nil {
				return err
			}
		}
		_, err = decoder.Token()
		return err
	case '[':
		if requireObject {
			return errors.New("JSON input must be an object")
		}
		for decoder.More() {
			if err := scanJSONValue(decoder, false); err != nil {
				return err
			}
		}
		_, err = decoder.Token()
		return err
	default:
		return errors.New("unexpected JSON delimiter")
	}
}
