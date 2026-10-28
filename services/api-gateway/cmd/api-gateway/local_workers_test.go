package main

import (
	"strings"
	"testing"
)

func TestLocalWorkerInputsAreExplicitAndDoNotAcceptProductionOrOtherTenants(t *testing.T) {
	settings := map[string]string{"EDUGRADE_PROVISION_WORKERS": "ocr,processing", "EDUGRADE_OCR_WORKER_TENANT_CODE": "platform", "EDUGRADE_OCR_WORKER_USERNAME": "ocr_worker", "EDUGRADE_OCR_WORKER_PASSWORD": strings.Repeat("a", 32), "EDUGRADE_PAGE_PROCESSING_TENANT_CODE": "platform", "EDUGRADE_PAGE_PROCESSING_USERNAME": "page_worker", "EDUGRADE_PAGE_PROCESSING_PASSWORD": strings.Repeat("b", 32)}
	get := func(key string) string { return settings[key] }
	workers, err := localWorkerInputs("local", get)
	if err != nil || len(workers) != 2 || workers[1].Username != "page_worker" {
		t.Fatalf("explicit inputs: %+v %v", workers, err)
	}
	if _, err = localWorkerInputs("production", get); err == nil {
		t.Fatal("production auto provisioning accepted")
	}
	settings["EDUGRADE_PROVISION_WORKERS"] = "ocr,ocr"
	if _, err = localWorkerInputs("local", get); err == nil {
		t.Fatal("duplicate capability accepted")
	}
	settings["EDUGRADE_PROVISION_WORKERS"] = "ocr"
	settings["EDUGRADE_OCR_WORKER_TENANT_CODE"] = "demo"
	if _, err = localWorkerInputs("local", get); err == nil {
		t.Fatal("non-platform tenant accepted")
	}
}
