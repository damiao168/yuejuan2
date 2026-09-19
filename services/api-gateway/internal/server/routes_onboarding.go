package server

import "net/http"

func registerOnboardingRoutes(mux *http.ServeMux, ctx routerContext) {
	mux.Handle("GET /api/v1/onboarding/readiness", ctx.guards.requireOnboardingRead(ctx.onboarding.Readiness))
}
