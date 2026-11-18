package server

import (
	"net/http"
	"testing"
)

// 夹具明确模拟在原页选择右上角区域；生产前端必须由教师逐页确认，不能沿用此合成坐标。
func e2eStory060ConfirmedPrintLayout(t *testing.T, router http.Handler, token, templateID string) map[string]any {
	t.Helper()
	context := e2eGetJSON(t, router, "/api/v1/answer-sheet-templates/"+templateID+"/print-context", token, http.StatusOK)["print_context"].(map[string]any)
	source := context["print_source"].(map[string]any)
	pages := []any{}
	for _, raw := range context["qr_layout_page_nos"].([]any) {
		pages = append(pages, map[string]any{"page_no": raw, "x": .82, "y": .005, "width": .16, "height": .09})
	}
	return map[string]any{"source_sha256": source["sha256"], "pages": pages}
}

// 迁移前批次没有确认记录。先补齐再下载；冻结后只能重放同一版式，扫描持锁期间不得抢先确认。
func e2eAssertLegacyPrintLayoutConfirmation(t *testing.T, scenario sheetIdentityScenario, batchID, sheetID string) {
	t.Helper()
	db, router, token, fixture := scenario.DB, scenario.Router, scenario.AdminToken, scenario.Acceptance
	if _, err := db.Exec(`UPDATE answer_sheet_print_batch SET print_layout=NULL WHERE tenant_id=$1 AND id=$2::uuid`, fixture.TenantID, batchID); err != nil {
		t.Fatal(err)
	}
	path := "/api/v1/answer-sheet-print-batches/" + batchID
	e2eExpectStatus(t, router, http.MethodGet, path+"/package.pdf", token, "", http.StatusConflict)
	layout := e2eStory060ConfirmedPrintLayout(t, router, token, fixture.TemplateID)
	body := story056JSON(t, layout)

	locked, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer locked.Rollback()
	if _, err = locked.Exec(`SELECT id FROM answer_sheet_print_sheet WHERE tenant_id=$1 AND id=$2::uuid FOR UPDATE`, fixture.TenantID, sheetID); err != nil {
		t.Fatal(err)
	}
	e2eExpectStatus(t, router, http.MethodPost, path+"/confirm-layout", token, body, http.StatusConflict)
	if err = locked.Rollback(); err != nil {
		t.Fatal(err)
	}

	e2ePostJSON(t, router, http.MethodPost, path+"/confirm-layout", token, body, http.StatusOK)
	e2eExpectStatus(t, router, http.MethodGet, path+"/package.pdf", token, "", http.StatusOK)
	e2ePostJSON(t, router, http.MethodPost, path+"/confirm-layout", token, body, http.StatusOK)
	layout["pages"].([]any)[0].(map[string]any)["x"] = .65
	e2eExpectStatus(t, router, http.MethodPost, path+"/confirm-layout", token, story056JSON(t, layout), http.StatusConflict)
}
