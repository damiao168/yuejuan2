package server

import "edugrade-enterprise/services/api-gateway/internal/schoolassistant"

type SchoolAssistantModule struct {
	Handler *schoolassistant.Handler
}

// NewSchoolAssistantModule 只接入领域读能力；查询服务不能借模块指针调用发布、改分或人员维护入口。
func NewSchoolAssistantModule(dependencies schoolassistant.Dependencies) *SchoolAssistantModule {
	return &SchoolAssistantModule{Handler: schoolassistant.NewHandler(dependencies)}
}
