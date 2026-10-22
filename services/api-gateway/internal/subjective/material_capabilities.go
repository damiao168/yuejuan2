package subjective

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
)

func (a *HTTPAdapterV2) materialCapability(ctx context.Context) (string, error) {
	return a.ReferenceMaterialCapability(ctx)
}

// ReferenceMaterialCapability 读取当前已认证实例的多图能力；不消费许可，也不调用模型。
// 能力不能跨实例长期缓存，每个实际请求仍带 wrapper 摘要并由接收实例检查。
func (a *HTTPAdapterV2) ReferenceMaterialCapability(ctx context.Context) (string, error) {
	if a == nil || a.base == nil || a.materials == nil || a.base.client == nil || a.base.baseURL == "" || a.base.token == "" || a.base.adapterType != "local_llama_cpp" {
		return "", &GradingAgentError{Code: "reference_material_capability_unavailable"}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.base.baseURL+"/grading/capabilities", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+a.base.token)
	// 即使同源跳转也不是当前实例的这个能力入口；不得跟随重定向扩大服务凭据的用途。
	client := *a.base.client
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := client.Do(req)
	if err != nil {
		return "", &GradingAgentError{Code: "reference_material_capability_unavailable"}
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return "", &GradingAgentError{Code: "reference_material_capability_unavailable"}
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, 16385))
	if err != nil || len(raw) > 16384 {
		return "", &GradingAgentError{Code: "reference_material_capability_unavailable"}
	}
	var capability struct {
		Features           []string `json:"features"`
		MathWrapperHash    string   `json:"math_wrapper_hash"`
		MaxReferenceImages int      `json:"max_reference_images"`
		Adapter            string   `json:"adapter"`
		Provider           string   `json:"provider"`
		Deployment         string   `json:"deployment"`
		Region             string   `json:"region"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&capability) != nil || !containsString(capability.Features, materialInputFeature) || !validLowerSHA256(capability.MathWrapperHash) || capability.MaxReferenceImages != 4 || capability.Adapter != a.base.adapterType || capability.Provider != a.base.providerKey || capability.Deployment != a.base.deploymentKey || capability.Region != a.base.deploymentRegion {
		return "", &GradingAgentError{Code: "reference_material_capability_unavailable"}
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return "", &GradingAgentError{Code: "reference_material_capability_unavailable"}
	}
	return capability.MathWrapperHash, nil
}
