package server

import (
	"net/http"
)

func registerSystemRoutes(mux *http.ServeMux, ctx routerContext) {
	mux.HandleFunc("GET /health", ctx.system.Health)
	mux.HandleFunc("GET /health/live", ctx.system.Health)
	mux.HandleFunc("GET /health/ready", ctx.system.Ready)
	mux.Handle("GET /metrics", ctx.metrics)
	// /ready remains permission-protected for backward compatibility. New
	// infrastructure probes must use the public, redacted /health/ready route.
	mux.Handle("GET /ready", ctx.guards.requireSystemRead(ctx.system.Ready))
	mux.Handle("GET /api/v1/system/info", ctx.guards.requireSystemRead(ctx.system.SystemInfo))
	mux.Handle("GET /api/v1/system/status", ctx.guards.requireSystemRead(ctx.system.SystemStatus))
	mux.Handle("GET /api/v1/ocr/availability", ctx.guards.requireOCRAvailabilityRead(ctx.system.OCRAvailability))
}
