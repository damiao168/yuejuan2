package binaryresource

import (
	"context"
	"io"
)

// Resource 描述已校验的私有对象。Open 延迟打开；图片条件请求和 HEAD 不得读取对象存储。
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
