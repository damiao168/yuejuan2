import { apiClient } from "./client";
import { buildQueryString } from "./query";

export interface Tenant {
  id: string;
  name: string;
  code: string;
  status: string;
}

export interface CreateTenantPayload {
  name: string;
  code: string;
  admin_username: string;
  admin_display_name: string;
  admin_password: string;
}

export interface School {
  id: string;
  tenant_id: string;
  name: string;
  code: string;
  education_stages?: Array<"junior" | "senior">;
  status: string;
}

export interface AcademicYear { id: string; tenant_id: string; school_id: string; name: string; start_year: number; end_year: number; starts_at: string; ends_at: string; is_current: boolean; status: string; }
export interface GradeCohort { id: string; tenant_id: string; school_id: string; education_stage: "junior" | "senior"; entry_year: number; expected_graduation_year: number; name: string; status: string; }

export interface Grade {
  id: string;
  tenant_id: string;
  school_id: string;
  name: string;
  level_no: number;
  academic_year: string;
  education_stage: "junior" | "senior";
  academic_year_id?: string;
  grade_cohort_id?: string;
  status: string;
}

export interface SchoolClass {
  id: string;
  tenant_id: string;
  school_id: string;
  grade_id: string;
  academic_year_id?: string;
  grade_cohort_id?: string;
  class_no?: number;
  name: string;
  code: string;
  status: string;
}

export interface Student {
  id: string;
  tenant_id: string;
  school_id: string;
  class_id: string;
  academic_year_id?: string;
  grade_cohort_id?: string;
  admission_year?: number;
  student_no: string;
  name: string;
  gender?: string;
  status: string;
}
export interface StudentEnrollment { id: string; student_id: string; school_id: string; academic_year_id: string; academic_year_name: string; grade_cohort_id: string; grade_cohort_name: string; class_id: string; class_name: string; status: string; start_date: string; end_date?: string; }

export interface StudentImportError {
  row: number;
  message: string;
}

export interface StudentImportResult {
  created: number;
  errors: StudentImportError[];
}

export async function createSchool(payload: Pick<School, "name" | "code">) {
  return apiClient.request<{ school: School }>("/api/v1/schools", {
    method: "POST",
    body: JSON.stringify({ ...payload, status: "active" })
  });
}

export async function listTenants(filter: { q?: string; limit?: number; cursor?: string } = {}) {
  return apiClient.request<{ tenants: Tenant[]; next_cursor: string; has_more: boolean }>(`/api/v1/tenants${buildQueryString(filter)}`);
}

export async function createTenant(payload: CreateTenantPayload) {
  return apiClient.request<{ tenant: Tenant }>("/api/v1/tenants", {
    method: "POST",
    body: JSON.stringify({ ...payload, status: "active" })
  });
}

export async function updateTenantStatus(id: string, status: "active" | "disabled") {
  return apiClient.request<{ tenant: Tenant }>(`/api/v1/tenants/${encodeURIComponent(id)}`, {
    method: "PATCH",
    body: JSON.stringify({ status })
  });
}

export async function listSchools() {
  return apiClient.request<{ schools: School[] }>("/api/v1/schools");
}

export async function listAcademicYears(schoolId?: string) {
  return apiClient.request<{ academic_years: AcademicYear[] }>(`/api/v1/academic-years${buildQueryString({ school_id: schoolId })}`);
}

export async function listGradeCohorts(schoolId?: string) {
  return apiClient.request<{ grade_cohorts: GradeCohort[] }>(`/api/v1/grade-cohorts${buildQueryString({ school_id: schoolId })}`);
}

export async function listGrades(schoolId?: string) {
  const query = schoolId ? `?school_id=${encodeURIComponent(schoolId)}` : "";
  return apiClient.request<{ grades: Grade[] }>(`/api/v1/grades${query}`);
}

export async function createGrade(payload: Pick<Grade, "school_id" | "name" | "level_no" | "academic_year" | "education_stage">) {
  return apiClient.request<{ grade: Grade }>("/api/v1/grades", {
    method: "POST",
    body: JSON.stringify({ ...payload, status: "active" })
  });
}

export function updateGrade(id: string, payload: Pick<Grade, "name">) {
  return apiClient.request<{ grade: Grade }>(`/api/v1/grades/${encodeURIComponent(id)}`, {
    method: "PATCH", body: JSON.stringify(payload)
  });
}

export async function listClasses(gradeId?: string) {
  const query = gradeId ? `?grade_id=${encodeURIComponent(gradeId)}` : "";
  return apiClient.request<{ classes: SchoolClass[] }>(`/api/v1/classes${query}`);
}

export async function createClass(payload: Pick<SchoolClass, "school_id" | "grade_id" | "name" | "code">) {
  return apiClient.request<{ class: SchoolClass }>("/api/v1/classes", {
    method: "POST",
    body: JSON.stringify({ ...payload, status: "active" })
  });
}

export function updateClass(id: string, payload: Pick<SchoolClass, "name" | "code">) {
  return apiClient.request<{ class: SchoolClass }>(`/api/v1/classes/${encodeURIComponent(id)}`, {
    method: "PATCH", body: JSON.stringify(payload)
  });
}

export async function bindTeacherClass(classId: string, teacherId: string) {
  return apiClient.request<{ status: string }>(`/api/v1/classes/${encodeURIComponent(classId)}/teachers`, {
    method: "POST",
    body: JSON.stringify({ teacher_id: teacherId })
  });
}

export async function listStudents(filter: { classId?: string; ids?: string[]; q?: string; limit?: number; cursor?: string } = {}) {
  const query = buildQueryString({ class_id: filter.classId, ids: filter.ids, q: filter.q, limit: filter.limit, cursor: filter.cursor });
  return apiClient.request<{ students: Student[]; next_cursor: string; has_more: boolean }>(`/api/v1/students${query}`);
}

export async function createStudent(payload: Pick<Student, "school_id" | "class_id" | "student_no" | "name"> & Partial<Pick<Student, "gender" | "status">>) {
  return apiClient.request<{ student: Student }>("/api/v1/students", {
    method: "POST",
    body: JSON.stringify({ ...payload, status: payload.status ?? "active" })
  });
}

export async function updateStudentStatus(id: string, status: "active" | "inactive") {
  return apiClient.request<{ student: Student }>(`/api/v1/students/${encodeURIComponent(id)}`, {
    method: "PATCH",
    body: JSON.stringify({ status })
  });
}

export function deleteStudent(id: string) {
  return apiClient.request<{ student_id: string; deleted: boolean }>(`/api/v1/students/${encodeURIComponent(id)}`, {
    method: "DELETE"
  });
}

export function updateStudent(id: string, payload: Pick<Student, "name" | "student_no"> & { gender: string }) {
  return apiClient.request<{ student: Student }>(`/api/v1/students/${encodeURIComponent(id)}`, {
    method: "PATCH", body: JSON.stringify(payload)
  });
}

export async function listStudentEnrollments(id: string) {
  return apiClient.request<{ enrollments: StudentEnrollment[] }>(`/api/v1/students/${encodeURIComponent(id)}/enrollments`);
}

export async function transferStudent(id: string, classId: string, startDate?: string) {
  return apiClient.request<{ enrollment: StudentEnrollment }>(`/api/v1/students/${encodeURIComponent(id)}/enrollment`, { method: "PUT", body: JSON.stringify({ class_id: classId, start_date: startDate }) });
}

export async function importStudentsCSV(csv: string) {
  return apiClient.request<{ result: StudentImportResult }>("/api/v1/students/import-csv", {
    method: "POST",
    headers: { "Content-Type": "text/csv; charset=utf-8" },
    body: csv
  });
}
