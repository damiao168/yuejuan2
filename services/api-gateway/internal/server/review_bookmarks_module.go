package server

import (
	"edugrade-enterprise/services/api-gateway/internal/config"
	"edugrade-enterprise/services/api-gateway/internal/reviewbookmark"
	"edugrade-enterprise/services/api-gateway/internal/segment"
)

// 组装时才绑定源能力，路由测试替换 Review/Seed 存储后也使用当前依赖。
// 生产工厂必须先提供持久化书签存储，不能因缺失接线退回内存。
func newPersonalReviewBookmarkHandler(cfg config.Config, stores GradingQualityStores, dependencies GradingQualityDependencies) *reviewbookmark.Handler {
	var provider reviewbookmark.SourceProvider
	if dependencies.DB != nil {
		provider = reviewbookmark.NewPostgresSourceProvider(dependencies.DB)
	} else {
		// 精确账号读取是可选能力；缺失时源提供器拒绝访问，不能凭旧会话放宽授权。
		users, _ := dependencies.Auth.(reviewbookmark.UserReader)
		provider = reviewbookmark.NewMemorySourceProvider(stores.Review, stores.SeedQuality,
			users, dependencies.Exams, dependencies.Schools, dependencies.Segments)
	}
	store := stores.ReviewBookmark
	switch current := store.(type) {
	case *reviewbookmark.MemoryStore:
		current.WithProvider(provider)
	case *reviewbookmark.PostgresStore:
		current.WithProvider(provider)
	}
	if store == nil && dependencies.DB == nil {
		store = reviewbookmark.NewMemoryStore(provider)
	}
	images, _ := dependencies.SegmentImage.(segment.VersionedCropImageReader)
	evidence := reviewbookmark.NewEvidenceService(provider, images, []byte(cfg.ModelSecrets.MasterKey))
	return reviewbookmark.NewHandler(store).WithEvidence(evidence)
}
