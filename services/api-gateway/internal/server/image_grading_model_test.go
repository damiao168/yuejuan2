package server

import (
	"context"
	"errors"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/modelgovernance"
	"github.com/google/uuid"
)

func TestImageGradingModelResolverRequiresExplicitSchoolSelectionAndTracksChanges(t *testing.T) {
	ctx := context.Background()
	store := modelgovernance.NewMemoryStore()
	school, actor := uuid.NewString(), uuid.NewString()
	input := modelgovernance.ManagedAPIConfigInput{
		ProviderKey: "custom", DisplayName: "合成视觉模型", AdapterType: "openai_compatible", BaseURL: "https://example.invalid/v1",
		ModelName: "image-model", ModelVersion: "v1", Region: "global", Status: "active", APIKey: "synthetic-image-key",
	}
	config, err := store.CreateManagedAPIConfig(ctx, school, actor, input)
	if err != nil {
		t.Fatal(err)
	}
	resolve := newImageGradingModelResolver(store)
	assertUnavailable := func(tenant string) {
		t.Helper()
		model, err := resolve(ctx, tenant)
		if model != nil || !errors.Is(err, modelgovernance.ErrGradingModelRequired) {
			t.Fatalf("unconfigured or disabled model accepted: %v", err)
		}
	}
	assertUnavailable(school)
	selection, err := store.SaveGradingModelSelection(ctx, school, actor, modelgovernance.DocumentModelSelectionInput{ExpectedRevision: "absent", Mode: "model", ModelConfigID: config.ID})
	if err != nil {
		t.Fatal(err)
	}
	model, err := resolve(ctx, school)
	if err != nil || model == nil || model.ConfigID != config.ID || model.APIKey != input.APIKey || model.ModelName != input.ModelName || model.ConfigRevision == "" {
		t.Fatalf("selected image model was not forwarded: %v", err)
	}
	assertUnavailable(uuid.NewString())
	before := model.ConfigRevision
	if _, err = store.SaveGradingModelSelection(ctx, school, actor, modelgovernance.DocumentModelSelectionInput{ExpectedRevision: selection.Revision, Mode: "model", ModelConfigID: config.ID}); err != nil {
		t.Fatal(err)
	}
	model, err = resolve(ctx, school)
	if err != nil || model.ConfigRevision == before {
		t.Fatalf("selection revision did not invalidate image evidence: %v", err)
	}
	before = model.ConfigRevision
	update := modelgovernance.ManagedAPIConfigUpdateInput{DisplayName: input.DisplayName, AdapterType: input.AdapterType, BaseURL: input.BaseURL, ModelName: input.ModelName, ModelVersion: input.ModelVersion, Region: input.Region, Status: input.Status, APIKey: "synthetic-rotated-image-key"}
	if _, err = store.UpdateManagedAPIConfig(ctx, school, config.ID, update); err != nil {
		t.Fatal(err)
	}
	model, err = resolve(ctx, school)
	if err != nil || model.ConfigRevision == before || model.APIKey != update.APIKey {
		t.Fatalf("credential change did not invalidate image evidence: %v", err)
	}
	update.Status = "disabled"
	if _, err = store.UpdateManagedAPIConfig(ctx, school, config.ID, update); err != nil {
		t.Fatal(err)
	}
	assertUnavailable(school)
}
