package port

import "context"

// MaterialReference is an opaque, owner-scoped protection fact. The owner
// never supplies a foreign key or business payload to Media.
type MaterialReference struct {
	MaterialKind    string
	MaterialID      int64
	Owner           string
	ReferenceDigest string
}

// MaterialReferenceRegistrar is the stable port future domains use to protect
// Media material. Register must fail if the locked target no longer exists.
type MaterialReferenceRegistrar interface {
	RegisterMediaReference(context.Context, MaterialReference) error
	UnregisterMediaReference(context.Context, MaterialReference) error
}
