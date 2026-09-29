package server

import "net/http"

// onboarding 只暴露聚合后的就绪状态，并限制在平台、租户或学校管理员，避免把底层组织明细变成公共探针。
func registerOnboardingRoutes(mux *http.ServeMux, ctx routerContext) {
	mux.Handle("GET /api/v1/onboarding/readiness", ctx.guards.requireOnboardingRead(ctx.onboarding.Readiness))
}
