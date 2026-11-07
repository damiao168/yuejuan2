package config

import (
	"fmt"
	"net/url"
)

func validateSchoolAssistantSearchURL(value string) error {
	if value == "" {
		return nil
	}
	endpoint, err := url.Parse(value)
	// 地址由部署者配置，可指向容器内的 HTTP 服务；凭据和查询参数不能混进搜索地址。
	if err != nil || endpoint.Hostname() == "" || (endpoint.Scheme != "http" && endpoint.Scheme != "https") || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" || endpoint.ForceQuery {
		return fmt.Errorf("EDUGRADE_SCHOOL_ASSISTANT_SEARCH_URL must be an HTTP(S) URL without credentials, query or fragment")
	}
	return nil
}
