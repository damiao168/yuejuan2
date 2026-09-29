package modelgovernance

import (
	"fmt"
	"strings"
)

// 启动前只检查会被路由的供应商和部署；外部配置的密钥、价格计量和健康状态任一不完整都阻止上线。
func ValidateProductionInventory(
	providers []Provider,
	deployments []Deployment,
	secrets SecretReferenceResolver,
) error {
	providerByTenantAndKey := make(map[string]Provider, len(providers))
	for _, provider := range providers {
		providerByTenantAndKey[provider.TenantID+"|"+provider.Key] = provider
		if provider.Kind != ProviderExternal || provider.Status == "disabled" {
			continue
		}
		if ValidateProvider(provider) != nil {
			return fmt.Errorf("%w: provider %s metadata is invalid", ErrProductionUnsafe, provider.Key)
		}
		probe, err := secrets.Probe(provider.CredentialRef)
		if err != nil {
			return fmt.Errorf("%w: provider %s has an invalid secret reference", ErrProductionUnsafe, provider.Key)
		}
		if probe.ResolverSupported && (!probe.Configured || !probe.MeetsMinimumStrength) {
			return fmt.Errorf("%w: provider %s secret is missing or weak", ErrProductionUnsafe, provider.Key)
		}
		if provider.Status != "unverified" && !probe.ResolverSupported {
			return fmt.Errorf("%w: provider %s has no installed secret resolver", ErrProductionUnsafe, provider.Key)
		}
	}
	for _, deployment := range deployments {
		provider, exists := providerByTenantAndKey[deployment.TenantID+"|"+deployment.ProviderKey]
		if !exists || provider.Kind != ProviderExternal || deployment.Status == "disabled" {
			continue
		}
		if ValidateDeployment(deployment, provider) != nil {
			return fmt.Errorf("%w: deployment %s metadata is invalid", ErrProductionUnsafe, deployment.Key)
		}
		meter := strings.TrimSpace(stringValue(deployment.PricingPolicy["meter"]))
		if meter == "" || meter == "not_configured" {
			return fmt.Errorf("%w: deployment %s has no pricing meter", ErrProductionUnsafe, deployment.Key)
		}
		if deployment.Status == "shadow_only" &&
			(provider.Status != "active" || deployment.HealthState != "available") {
			return fmt.Errorf("%w: routable deployment %s is not fully verified", ErrProductionUnsafe, deployment.Key)
		}
	}
	return nil
}
