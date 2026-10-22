package server

import (
	"net/http"
)

// 租户变更和组织数据都经过组织权限；创建租户等高影响操作额外要求近期认证。
func registerOrganizationRoutes(mux *http.ServeMux, ctx routerContext) {
	mux.Handle("POST /api/v1/tenants", ctx.guards.requireRecentPermission("tenant:manage", ctx.modules.Identity.OrgHandler.CreateTenant))
	mux.Handle("GET /api/v1/tenants", ctx.guards.requireAuth(http.HandlerFunc(ctx.modules.Identity.OrgHandler.ListTenants)))
	mux.Handle("PATCH /api/v1/tenants/{id}", ctx.guards.requireRecentPermission("tenant:manage", ctx.modules.Identity.OrgHandler.UpdateTenant))
	mux.Handle("POST /api/v1/schools", ctx.guards.requireOrgManage(ctx.modules.Identity.OrgHandler.CreateSchool))
	mux.Handle("GET /api/v1/schools", ctx.guards.requireOrgManage(ctx.modules.Identity.OrgHandler.ListSchools))
	mux.Handle("GET /api/v1/academic-years", ctx.guards.requireOrgManage(ctx.modules.Identity.OrgHandler.ListAcademicYears))
	mux.Handle("GET /api/v1/grade-cohorts", ctx.guards.requireOrgManage(ctx.modules.Identity.OrgHandler.ListGradeCohorts))
	mux.Handle("POST /api/v1/grades", ctx.guards.requireOrgManage(ctx.modules.Identity.OrgHandler.CreateGrade))
	mux.Handle("GET /api/v1/grades", ctx.guards.requireOrgManage(ctx.modules.Identity.OrgHandler.ListGrades))
	// 资料更新沿用对象边界检查，只接受可编辑字段，不能借改名迁移学校或学年。
	mux.Handle("PATCH /api/v1/grades/{id}", ctx.guards.requireOrgManage(ctx.modules.Identity.OrgHandler.UpdateGrade))
	mux.Handle("POST /api/v1/classes", ctx.guards.requireOrgManage(ctx.modules.Identity.OrgHandler.CreateClass))
	mux.Handle("GET /api/v1/classes", ctx.guards.requireOrgManage(ctx.modules.Identity.OrgHandler.ListClasses))
	mux.Handle("PATCH /api/v1/classes/{id}", ctx.guards.requireOrgManage(ctx.modules.Identity.OrgHandler.UpdateClass))
	mux.Handle("POST /api/v1/students", ctx.guards.requireOrgManage(ctx.modules.Identity.OrgHandler.CreateStudent))
	mux.Handle("GET /api/v1/students", ctx.guards.requireOrgManage(ctx.modules.Identity.OrgHandler.ListStudents))
	mux.Handle("PATCH /api/v1/students/{id}", ctx.guards.requireOrgManage(ctx.modules.Identity.OrgHandler.UpdateStudent))
	mux.Handle("DELETE /api/v1/students/{id}", ctx.guards.requireRecentPermission("org:manage", ctx.modules.Identity.OrgHandler.DeleteStudent))
	mux.Handle("GET /api/v1/students/{id}/enrollments", ctx.guards.requireOrgManage(ctx.modules.Identity.OrgHandler.ListStudentEnrollments))
	mux.Handle("PUT /api/v1/students/{id}/enrollment", ctx.guards.requireOrgManage(ctx.modules.Identity.OrgHandler.TransferStudent))
	mux.Handle("POST /api/v1/students/import-csv", ctx.guards.requireStudentImport(ctx.modules.Identity.OrgHandler.ImportStudentsCSV))
	mux.Handle("POST /api/v1/classes/{id}/teachers", ctx.guards.requireOrgManage(ctx.modules.Identity.OrgHandler.BindTeacherClass))
}
