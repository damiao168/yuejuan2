package binaryresource

import (
	"context"
	"io"
)

// Resource describes a validated private object. Open is deliberately lazy:
// conditional image requests and HEAD must not read object storage.
type Resource struct {
	ContentType      string
	Size             int64
	Disposition      string
	ETag             string
	CacheControl     string
	AllowHead        bool
	Open             func(context.Context) (io.ReadCloser, error)
	OpenErrorMessage string
	AuditAction      string
	AuditTargetType  string
	AuditTargetID    string
	AuditReason      string
}
