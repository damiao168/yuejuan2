package modelgovernance

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/auth"
)

func TestEnvironmentSecretResolverProbesWithoutReturningSecret(t *testing.T) {
	t.Setenv("STORY061_VENDOR_KEY", "super-secret-value-at-least-24-bytes")
	resolver := NewEnvironmentSecretResolver(t.TempDir())
	probe, err := resolver.Probe("env://STORY061_VENDOR_KEY")
	if err != nil {
		t.Fatalf("probe configured environment reference: %v", err)
	}
	if probe.Scheme != "env" || !probe.ResolverSupported || !probe.Configured || !probe.MeetsMinimumStrength {
		t.Fatalf("unexpected probe: %#v", probe)
	}
}

func TestEnvironmentSecretResolverRejectsTraversalAndPlaintext(t *testing.T) {
	resolver := NewEnvironmentSecretResolver(t.TempDir())
	for _, reference := range []string{
		"actual-secret-value",
		"docker_secret://../secret",
		"env://folder/secret",
	} {
		if _, err := resolver.Probe(reference); !errors.Is(err, ErrInvalidSecretReference) {
			t.Fatalf("expected invalid reference for %q, got %v", reference, err)
		}
	}
}

func TestEnvironmentSecretResolverDoesNotClaimUnconfiguredManagers(t *testing.T) {
	resolver := NewEnvironmentSecretResolver(t.TempDir())
	probe, err := resolver.Probe("vault://edugrade/vendor-a")
	if err != nil {
		t.Fatalf("probe valid future resolver reference: %v", err)
	}
	if probe.Scheme != "vault" || probe.ResolverSupported || probe.Configured {
		t.Fatalf("unsupported resolver must fail closed: %#v", probe)
	}
}

// 已登记的下划线协议是产品引用约定；不能由标准 URL scheme 校验提前拒绝本地文件或云引用。
func TestSecretReferencesPreserveDocumentedSchemesThroughProviderHandler(t *testing.T) {
	secretDir := t.TempDir()
	const value = "round20-synthetic-secret-32-bytes"
	t.Setenv("ROUND20_SECRET_REFERENCE_KEY", value)
	if err := os.WriteFile(filepath.Join(secretDir, "round20-key"), []byte(value), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		scheme    string
		reference string
		supported bool
	}{
		{"env", "env://ROUND20_SECRET_REFERENCE_KEY", true},
		{"docker_secret", "docker_secret://round20-key", true},
		{"vault", "vault://edugrade/team/vendor-a", false},
		{"aws_secrets_manager", "aws_secrets_manager://region/team/vendor-a", false},
		{"azure_key_vault", "azure_key_vault://vault-name/secrets/vendor-a", false},
		{"gcp_secret_manager", "gcp_secret_manager://project/secrets/vendor-a", false},
	} {
		t.Run(test.scheme, func(t *testing.T) {
			resolver := NewEnvironmentSecretResolver(secretDir)
			probe, err := resolver.Probe(test.reference)
			if err != nil || probe.Scheme != test.scheme || probe.ResolverSupported != test.supported || probe.Configured != test.supported || probe.MeetsMinimumStrength != test.supported {
				t.Errorf("documented reference rejected or misreported: probe=%+v err=%v", probe, err)
			}
			provider := productionExternalProvider(test.reference)
			if err = ValidateProvider(provider); err != nil {
				t.Errorf("provider validator rejected documented reference: %v", err)
			}
			if err = ValidateProductionInventory([]Provider{provider}, nil, resolver); err != nil {
				t.Errorf("unverified provider cannot be staged: %v", err)
			}
			if !test.supported {
				provider.Status = "active"
				if err = ValidateProductionInventory([]Provider{provider}, nil, resolver); !errors.Is(err, ErrProductionUnsafe) {
					t.Errorf("uninstalled cloud resolver activated: %v", err)
				}
			}
			body, err := json.Marshal(map[string]any{
				"provider_key": "round20-vendor", "display_name": "Round20 Vendor", "provider_kind": "external",
				"adapter_type": "vendor_a_native", "credential_ref": test.reference, "region": "cn-east",
				"data_policy": map[string]any{"training_allowed": false, "retention_mode": "no_store"},
			})
			if err != nil {
				t.Fatal(err)
			}
			handler := NewHandler(NewMemoryStore(), nil, resolver, testBaseline())
			user := auth.User{ID: "round20-actor", TenantID: auth.PlatformTenantID, Permissions: []string{"model:provider:manage"}}
			response := performHandlerRequest(t, user, http.MethodPost, "/api/v1/model-providers", string(body), handler.CreateProvider)
			if response.Code != http.StatusCreated {
				t.Errorf("actual CreateProvider rejected documented reference: status=%d body=%s", response.Code, response.Body.String())
			}
			if strings.Contains(response.Body.String(), test.reference) || strings.Contains(response.Body.String(), value) {
				t.Error("provider response exposed secret reference or value")
			}
		})
	}
}

func TestDockerSecretReferenceReportsMetadataWithoutWeakSecretAdmission(t *testing.T) {
	root := t.TempDir()
	for name, value := range map[string]string{"strong": strings.Repeat("x", 32), "weak": "short", "empty": ""} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(value), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(root, "directory"), 0o700); err != nil {
		t.Fatal(err)
	}
	resolver := NewEnvironmentSecretResolver(root)
	for _, test := range []struct {
		name       string
		configured bool
		strong     bool
	}{
		{"strong", true, true}, {"weak", true, false}, {"empty", false, false},
		{"directory", false, false}, {"missing", false, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			reference := "docker_secret://" + test.name
			probe, err := resolver.Probe(reference)
			if err != nil || probe.Scheme != "docker_secret" || !probe.ResolverSupported || probe.Configured != test.configured || probe.MeetsMinimumStrength != test.strong {
				t.Fatalf("docker file metadata: probe=%+v err=%v", probe, err)
			}
			err = ValidateProductionInventory([]Provider{productionExternalProvider(reference)}, nil, resolver)
			if test.strong && err != nil || !test.strong && !errors.Is(err, ErrProductionUnsafe) {
				t.Fatalf("production admission differs from file metadata: %v", err)
			}
		})
	}
}

// 云引用可带层级路径；所有协议仍拒绝身份信息、参数和穿越，本地环境变量/文件只接受单个名称。
func TestSecretReferencesRejectUnsafeAuthorityAndPathForms(t *testing.T) {
	resolver := NewEnvironmentSecretResolver(t.TempDir())
	for _, reference := range []string{
		"plaintext-value", "unknown://secret", "env://user@KEY", "env://folder/KEY",
		"docker_secret://user@key", "docker_secret://key?version=1", "docker_secret://key?",
		"docker_secret://key#fragment", "docker_secret://key#", "docker_secret://folder/key",
		"docker_secret://../key", "docker_secret://%2e%2e/key", "docker_secret://folder%2fkey",
		"vault://namespace/../key", "vault://namespace/%2e%2e/key",
		"aws_secrets_manager://user@region/key", "azure_key_vault://vault/key?version=1",
		"gcp_secret_manager://project/key#version",
	} {
		t.Run(reference, func(t *testing.T) {
			if _, err := resolver.Probe(reference); !errors.Is(err, ErrInvalidSecretReference) {
				t.Errorf("unsafe reference accepted by resolver: %v", err)
			}
			if validCredentialReference(reference) {
				t.Error("unsafe reference accepted by provider validator")
			}
		})
	}
}
