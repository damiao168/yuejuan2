package modelgovernance

import (
	"context"
	"net/http"
	"testing"
	"time"
)

func TestManagedOrdinaryUpdateRequiresClientRevision(t *testing.T) {
	for _, mode := range []string{"missing", "malformed", "stale", "current"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			store := NewMemoryStore()
			const tenant = "00000000-0000-0000-0000-000000000020"
			item, err := store.CreateManagedAPIConfig(ctx, tenant, "actor", ManagedAPIConfigInput{
				ProviderKey: "synthetic", DisplayName: "Original", AdapterType: "openai_compatible", BaseURL: "https://revision.example/v1",
				APIKey: "synthetic-revision-key-123456", ModelName: "model", ModelVersion: "v1", Region: "global", Status: "active",
			})
			if err != nil {
				t.Fatal(err)
			}
			input := ManagedAPIConfigUpdateInput{DisplayName: "Changed", AdapterType: item.AdapterType, BaseURL: item.BaseURL,
				ModelName: item.ModelName, ModelVersion: item.ModelVersion, Region: item.Region, Status: item.Status,
				ClientExpectedUpdatedAt: item.UpdatedAt.Format(time.RFC3339Nano)}
			want := http.StatusOK
			switch mode {
			case "missing":
				input.ClientExpectedUpdatedAt = ""
				want = http.StatusBadRequest
			case "malformed":
				input.ClientExpectedUpdatedAt = "not-a-time"
				want = http.StatusBadRequest
			case "stale":
				input.ClientExpectedUpdatedAt = item.UpdatedAt.Add(-time.Second).Format(time.RFC3339Nano)
				want = http.StatusConflict
			}
			handler := NewHandler(store, nil, NewEnvironmentSecretResolver(t.TempDir()), testBaseline())
			response := reviewManagedUpdate(t, handler, tenant, item.ID, input)
			if response.Code != want {
				t.Fatalf("status=%d want=%d body=%s", response.Code, want, response.Body.String())
			}
			current, err := store.GetManagedAPIConnection(ctx, tenant, item.ID)
			if err != nil {
				t.Fatal(err)
			}
			if want != http.StatusOK && (current.Config.DisplayName != item.DisplayName || !current.Config.UpdatedAt.Equal(item.UpdatedAt)) {
				t.Fatal("rejected client revision changed stored configuration")
			}
		})
	}
}
