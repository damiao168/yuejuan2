# AI grading V1/V2 usage and retirement

Status: inventory and measurement in place; V1 retirement is not approved.

## Runtime inventory

| Entry | Current contract path | Where selected |
| --- | --- | --- |
| `POST /api/v1/answer-segments/{id}/subjective-ai-grade` | V1 `/grading/grade` by default; V2 `/grading/grade-v2` for eligible mathematics when enabled | `subjective/handlers_grade.go` → `buildAdapterInput` |
| Subjective grading batch worker | Same selection as synchronous grading | `subjective/handlers_worker.go` `ExecuteWorker` → `buildAdapterInput`; exposed at `POST /api/v1/internal/subjective-grading/runs/{runId}/execute` |
| A/B/C panel grading library | Calls a bound adapter; mathematics requires a V2-capable binding, verified math evidence and an active crop | `subjective/panel_orchestrator.go` and `subjective/math_grading.go`; no production route constructing this orchestrator was found |
| Grading agent HTTP server | Both `/grading/grade` and `/grading/grade-v2` are live | `ai-services/grading_agent/server.py` dispatches to `GradingAgentApplication.grade` or `grade_v2` |

The gateway builds a V1 HTTP adapter whenever real AI service is configured. It builds a V2 adapter when `EDUGRADE_MATH_GRADING_V2_ENABLED=true`. V2 still requires mathematics, a verified effective math artifact and a matching active crop. Other subjects continue on V1. Both checked-in `.env.example` and the main Compose configuration default the V2 flag to `false`. `app.py` also remains the entry point for V2 through `ProductionMathV2Application` in `app_v2.py`; removing `app.py` would remove both routes.

The standalone subjective grading worker calls the gateway's internal execute endpoint. It does not call the Python agent directly. Completion and failure callbacks carry run results; the gateway remains responsible for selecting and validating the contract. Existing result envelopes use `subjective-grade-v1` and `math-grade-v2`; historical result parsing must be checked separately from live inference before deletion.

## Test coverage and known gaps

- Go adapter tests cover V1 `/grading/grade`, V2 `/grading/grade-v2`, contract rejection, and the opt-in real local V1 agent test in `adapter_http_real_e2e_test.go`.
- Python tests cover each HTTP route in `test_http.py` and `test_math_v2_production.py`.
- Story-060 Compose E2E starts the standalone worker and agent but does not set `EDUGRADE_MATH_GRADING_V2_ENABLED`; it exercises the V1 path. The browser `subjective-command-recovery.spec.ts` mocks batch command recovery and does not prove a live V2 inference.
- A production-like worker E2E with the V2 flag enabled, verified math evidence, and real `/grading/grade-v2` traffic is still required before retiring V1. Non-math production entry points require a V2 contract and migration plan first.

## Measurement

The gateway's existing `/metrics` exporter now includes `edugrade_ai_grading_requests_total{ai_contract_version="v1|v2",outcome="success|error"}`. Both versions and outcomes start at zero so absence of a series is not mistaken for zero usage. Each series counts one **logical V1/V2 HTTP adapter call** that reached an HTTP attempt, after retries and response validation. Local admission or request-building rejection does not increment it. Other adapter implementations and direct calls to the Python service that bypass the gateway are outside this metric; verify bindings, ingress and service access logs before claiming zero V1 traffic.

Example release-window queries (choose the actual release duration):

```promql
sum by (ai_contract_version) (increase(edugrade_ai_grading_requests_total[7d]))
sum by (ai_contract_version, outcome) (increase(edugrade_ai_grading_requests_total[7d]))
```

The only dynamic observation is the count. Version and outcome labels are fixed, with no tenant, student, answer, request, model, or endpoint identifiers.

## Retirement gates

1. Move every production scoring entry, including non-math and panel bindings, to a validated V2 successor. Keep server-owned score settlement and teacher review behavior.
2. Run both synchronous and standalone-worker E2E on V2 with real agent transport. Verify retry, failure, completion, and historical result reads.
3. Confirm the gateway V1 success and error counters stay at zero **increase** across consecutive releases with reliable scraping and no monitoring gaps. Check direct agent ingress separately.
4. Confirm persisted V1 grades, runs, and worker result envelopes remain readable without the V1 inference runtime. Keep historical schema/record readers if needed.
5. Only then remove live V1 route, V1 adapter and inference code in a separate change, with a rollback plan and a release observation window.

At present, the V2 flag defaults off and non-math subjects have no V2 route. The gates above are therefore unmet.
