package server

import (
	"strings"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/config"
	"edugrade-enterprise/services/api-gateway/internal/files"
	"edugrade-enterprise/services/api-gateway/internal/processing"
)

func TestTransactionalApplicationCompositionRejectsMissingCoordinatorCapability(t *testing.T) {
	stores := NewMemoryApplicationStores()
	_, err := NewTransactionalCaptureProcessingModule(config.Config{}, stores.Capture, captureProcessingTestDependencies(stores))
	if err == nil || !strings.Contains(err.Error(), "transactional command coordination") {
		t.Fatalf("production composition accepted a store without required capability: %v", err)
	}
}

func TestTransactionalCaptureCompositionRejectsTypedNilCapability(t *testing.T) {
	stores := NewMemoryApplicationStores()
	dependencies := captureProcessingTestDependencies(stores)
	var missing *auth.MemoryStore
	dependencies.Auth = missing
	_, err := NewTransactionalCaptureProcessingModule(config.Config{}, stores.Capture, dependencies)
	if err == nil || !strings.Contains(err.Error(), "capture dependency Auth is not configured") {
		t.Fatalf("production composition accepted a typed nil dependency: %v", err)
	}
}

func TestTransactionalApplicationRejectsTypedNilBeforeConstruction(t *testing.T) {
	stores := NewMemoryApplicationStores()
	var missing *auth.MemoryStore
	stores.Identity.Auth = missing
	_, err := NewTransactionalApplicationModules(ApplicationDependencies{}, stores)
	if err == nil || !strings.Contains(err.Error(), "stores.Identity.Auth is not configured") {
		t.Fatalf("production composition accepted a typed nil store: %v", err)
	}
}

func TestProductionStoreGraphRejectsMissingStores(t *testing.T) {
	stores := NewMemoryApplicationStores()
	stores.Identity.Auth = nil
	err := validatePostgresStoreGraph(stores)
	if err == nil || !strings.Contains(err.Error(), "stores.Identity.Auth is not configured") {
		t.Fatalf("production graph accepted a missing store: %v", err)
	}
}

func captureProcessingTestDependencies(stores ApplicationStores) CaptureProcessingDependencies {
	return CaptureProcessingDependencies{
		Auth: stores.Identity.Auth, Exams: stores.Exam.Exam, Files: stores.Exam.Files,
		Objects: files.NewMemoryObjectStorage(), Submissions: stores.Exam.Submissions,
		Processing: processing.NewService(stores.Capture.Processing, stores.Capture.WorkerRuntime),
	}
}
