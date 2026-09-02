package port

import "context"

// ImageMetadataReader and AttachmentMetadataReader are narrow Media-owned
// compatibility seams. They expose existence only so fixed-content references
// can fail closed; they do not expose blobs, URLs, provider IDs, or stores.
// Terra adapts them to the canonical Media port when Media is integrated.
type ImageMetadataReader interface {
	ImageExists(context.Context, int64) (bool, error)
}

type AttachmentMetadataReader interface {
	AttachmentExists(context.Context, int64) (bool, error)
}
