package platformschools

import "context"

type Store interface {
	ListSummaries(context.Context, int) ([]PlatformSchoolSummary, error)
	ListMembers(context.Context, string) ([]Member, error)
	GetUsage(context.Context, string, UsageRange) (UsageResult, error)
	GetModelHealth(context.Context, string) (ModelHealthResult, error)
	GetActivity(context.Context, string, int) (ActivityResult, error)
}
