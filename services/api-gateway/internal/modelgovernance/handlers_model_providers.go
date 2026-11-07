package modelgovernance

import (
	"edugrade-enterprise/services/api-gateway/internal/httpx"
	"net/http"
	"strings"
)

func (h *Handler) GetCurrentPrompt(w http.ResponseWriter, r *http.Request) {
	if h.prompts == nil {
		httpx.Error(w, r, http.StatusServiceUnavailable, "runtime_prompt_unavailable", "评分服务当前不可用")
		return
	}
	prompt, err := h.prompts.Current(r.Context())
	if err != nil {
		httpx.Error(w, r, http.StatusServiceUnavailable, "runtime_prompt_unavailable", "无法读取评分服务当前提示词")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"prompt": prompt})
}

func (h *Handler) ListProviders(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := h.targetTenant(w, r, r.URL.Query().Get("tenant_id"))
	if !ok {
		return
	}
	items, err := h.store.ListProviders(r.Context(), tenantID)
	if err != nil {
		httpx.Error(w, r, http.StatusInternalServerError, "model_provider_list_failed", "failed to list model providers")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"providers": items})
}

func (h *Handler) CreateProvider(w http.ResponseWriter, r *http.Request) {
	var input ProviderInput
	if !decodeStrictJSON(w, r, &input) {
		return
	}
	tenantID, ok := h.targetTenant(w, r, input.TenantID)
	if !ok {
		return
	}
	provider := ProviderFromInput(input)
	if provider.Status == "" {
		provider.Status = "unverified"
		input.Status = provider.Status
	}
	if strings.TrimSpace(input.DisplayName) == "" || ValidateProvider(provider) != nil {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_model_provider", "invalid model provider configuration")
		return
	}
	if provider.Kind == ProviderExternal {
		probe, err := h.secrets.Probe(provider.CredentialRef)
		if err != nil {
			httpx.Error(w, r, http.StatusBadRequest, "invalid_secret_reference", "credential_ref must be a supported secret reference URI")
			return
		}
		if provider.Status != "unverified" && provider.Status != "disabled" {
			httpx.Error(w, r, http.StatusConflict, "external_provider_not_verified", "external providers cannot be activated before native adapter verification")
			return
		}
		if provider.Status != "disabled" && probe.ResolverSupported && !probe.Configured {
			httpx.Error(w, r, http.StatusConflict, "secret_not_configured", "the referenced secret is not configured")
			return
		}
	}
	user := mustUser(r)
	actorID := user.ID
	if tenantID != user.TenantID {
		actorID = ""
	}
	item, err := h.store.CreateProvider(r.Context(), tenantID, actorID, input)
	if err != nil {
		writeStoreError(w, r, err, "model_provider_create_failed", "failed to create model provider")
		return
	}
	h.auditAction(r, "model.provider_created", "model_provider", item.ID, input.Reason(),
		map[string]any{"tenant_id": tenantID, "provider_key": item.Key, "provider_kind": item.Kind, "adapter_type": item.AdapterType, "region": item.Region, "status": item.Status})
	httpx.JSON(w, http.StatusCreated, map[string]any{"provider": item})
}

func (h *Handler) UpdateProviderStatus(w http.ResponseWriter, r *http.Request) {
	var input ProviderStatusInput
	if !decodeStrictJSON(w, r, &input) {
		return
	}
	if strings.TrimSpace(input.Reason) == "" || !validProviderStatus(input.Status) {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_model_provider_status", "valid status and reason are required")
		return
	}
	tenantID, ok := h.targetTenant(w, r, r.URL.Query().Get("tenant_id"))
	if !ok {
		return
	}
	providers, err := h.store.ListProviders(r.Context(), tenantID)
	if err != nil {
		httpx.Error(w, r, http.StatusInternalServerError, "model_provider_list_failed", modelProviderInspectionFailed)
		return
	}
	current, exists := providerByID(providers, r.PathValue("id"))
	if !exists {
		httpx.Error(w, r, http.StatusNotFound, "model_provider_not_found", "model provider not found")
		return
	}
	if current.Kind == ProviderExternal && input.Status != "unverified" && input.Status != "disabled" {
		httpx.Error(w, r, http.StatusConflict, "external_provider_not_verified", "external providers cannot be activated before native adapter verification")
		return
	}
	item, err := h.store.UpdateProviderStatus(r.Context(), tenantID, current.ID, input)
	if err != nil {
		writeStoreError(w, r, err, "model_provider_update_failed", "failed to update model provider")
		return
	}
	h.auditAction(r, "model.provider_status_updated", "model_provider", item.ID, input.Reason,
		map[string]any{"tenant_id": tenantID, "provider_key": item.Key, "before_status": current.Status, "status": item.Status})
	httpx.JSON(w, http.StatusOK, map[string]any{"provider": item})
}

func (h *Handler) ListDeployments(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := h.targetTenant(w, r, r.URL.Query().Get("tenant_id"))
	if !ok {
		return
	}
	items, err := h.store.ListDeployments(r.Context(), tenantID)
	if err != nil {
		httpx.Error(w, r, http.StatusInternalServerError, "model_deployment_list_failed", "failed to list model deployments")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"deployments": items})
}

func (h *Handler) CreateDeployment(w http.ResponseWriter, r *http.Request) {
	var input DeploymentInput
	if !decodeStrictJSON(w, r, &input) {
		return
	}
	tenantID, ok := h.targetTenant(w, r, input.TenantID)
	if !ok {
		return
	}
	if input.Status == "" {
		input.Status = "unverified"
	}
	if input.HealthState == "" {
		input.HealthState = "unverified"
	}
	providers, err := h.store.ListProviders(r.Context(), tenantID)
	if err != nil {
		httpx.Error(w, r, http.StatusInternalServerError, "model_provider_list_failed", modelProviderInspectionFailed)
		return
	}
	provider, exists := providerByID(providers, input.ProviderID)
	if !exists {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_model_provider", "provider_id does not belong to the target tenant")
		return
	}
	deployment := DeploymentFromInput(input, provider.Key)
	if strings.TrimSpace(input.ModelName) == "" ||
		ValidateDeployment(deployment, provider) != nil ||
		len(input.PricingPolicy) == 0 ||
		strings.TrimSpace(stringValue(input.PricingPolicy["meter"])) == "" {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_model_deployment", "invalid model deployment configuration")
		return
	}
	if provider.Kind == ProviderExternal &&
		(input.Status != "unverified" || input.HealthState != "unverified") {
		httpx.Error(w, r, http.StatusConflict, "external_deployment_not_verified", "external deployments must remain unverified until native adapter acceptance")
		return
	}
	user := mustUser(r)
	actorID := user.ID
	if tenantID != user.TenantID {
		actorID = ""
	}
	item, err := h.store.CreateDeployment(r.Context(), tenantID, actorID, input)
	if err != nil {
		writeStoreError(w, r, err, "model_deployment_create_failed", "failed to create model deployment")
		return
	}
	h.auditAction(r, "model.deployment_created", "model_deployment", item.ID, "create governed model deployment",
		map[string]any{"tenant_id": tenantID, "provider_key": item.ProviderKey, "deployment_key": item.Key, "region": item.Region, "status": item.Status, "health_state": item.HealthState})
	httpx.JSON(w, http.StatusCreated, map[string]any{"deployment": item})
}

func (h *Handler) UpdateDeploymentState(w http.ResponseWriter, r *http.Request) {
	var input DeploymentStateInput
	if !decodeStrictJSON(w, r, &input) {
		return
	}
	if strings.TrimSpace(input.Reason) == "" || !validDeploymentStatus(input.Status) || !validHealthState(input.HealthState) {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_model_deployment_state", "valid status, health_state and reason are required")
		return
	}
	tenantID, ok := h.targetTenant(w, r, r.URL.Query().Get("tenant_id"))
	if !ok {
		return
	}
	deployments, err := h.store.ListDeployments(r.Context(), tenantID)
	if err != nil {
		httpx.Error(w, r, http.StatusInternalServerError, "model_deployment_list_failed", "failed to inspect model deployment")
		return
	}
	current, exists := deploymentByID(deployments, r.PathValue("id"))
	if !exists {
		httpx.Error(w, r, http.StatusNotFound, "model_deployment_not_found", "model deployment not found")
		return
	}
	providers, err := h.store.ListProviders(r.Context(), tenantID)
	if err != nil {
		httpx.Error(w, r, http.StatusInternalServerError, "model_provider_list_failed", modelProviderInspectionFailed)
		return
	}
	provider, exists := providerByID(providers, current.ProviderID)
	if !exists {
		httpx.Error(w, r, http.StatusConflict, "model_provider_not_found", "deployment provider is unavailable")
		return
	}
	externalStateAllowed := input.Status == "unverified" && input.HealthState == "unverified" ||
		input.Status == "disabled" &&
			(input.HealthState == "unverified" || input.HealthState == "unavailable" || input.HealthState == "disabled")
	if provider.Kind == ProviderExternal && !externalStateAllowed {
		httpx.Error(w, r, http.StatusConflict, "external_deployment_not_verified", "external deployments cannot enter routing before native adapter acceptance")
		return
	}
	item, err := h.store.UpdateDeploymentState(r.Context(), tenantID, current.ID, input)
	if err != nil {
		writeStoreError(w, r, err, "model_deployment_update_failed", "failed to update model deployment")
		return
	}
	h.auditAction(r, "model.deployment_state_updated", "model_deployment", item.ID, input.Reason,
		map[string]any{"tenant_id": tenantID, "deployment_key": item.Key, "before_status": current.Status, "status": item.Status, "before_health_state": current.HealthState, "health_state": item.HealthState})
	httpx.JSON(w, http.StatusOK, map[string]any{"deployment": item})
}
