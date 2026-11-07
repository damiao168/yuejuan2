import { examSubjectLabel, examSubjectOptions } from "./examStatus";

export function normalizeTeachingSubject(subject: string): string {
  const code = subject.trim().toLowerCase();
  const aliases: Record<string, string> = {
    math: "mathematics", 数学: "mathematics",
    politics: "ethics_politics", civics: "ethics_politics", 政治: "ethics_politics", 道德与法治: "ethics_politics", 思想政治: "ethics_politics",
    语文: "chinese", 英语: "english", 物理: "physics", 化学: "chemistry", 生物: "biology", 历史: "history", 地理: "geography"
  };
  return aliases[code] ?? code;
}

export function normalizeTeachingSubjects(subjects: readonly string[] = []): string[] {
  return [...new Set(subjects.map(normalizeTeachingSubject).filter(Boolean))].sort();
}

export const teachingSubjectOptions = examSubjectOptions.map(option => ({
  ...option, value: normalizeTeachingSubject(option.value)
}));
const knownSubjects = new Set(teachingSubjectOptions.map(option => option.value));

export function isSubjectManagedTeacher(roles: readonly string[]): boolean {
  // 管理员的学校管理范围不由任教学科决定，兼任教师时也不在资料表单中改写其授权。
  return !roles.some(role => ["school_admin", "tenant_admin", "platform_admin"].includes(role))
    && roles.some(role => ["teacher", "grader", "arbitrator"].includes(role));
}

export function teachingSubjectsLabel(subjects: readonly string[] = []): string {
  return normalizeTeachingSubjects(subjects).map(examSubjectLabel).join("、") || "未设置";
}

export function canTeachSubjects(user: { roles: string[]; teaching_subjects?: string[] }, subjects: readonly string[]): boolean {
  if (user.roles.some(role => ["school_admin", "tenant_admin", "platform_admin"].includes(role))) return true;
  if (!isSubjectManagedTeacher(user.roles)) return false;
  const teaching = new Set(normalizeTeachingSubjects(user.teaching_subjects));
  // 一次分配可能跨题、跨考试，教师必须能任教本次所有学科；未知学科不能推测为可分配。
  return subjects.length > 0 && subjects.every(subject => {
    const code = normalizeTeachingSubject(subject);
    return knownSubjects.has(code) && teaching.has(code);
  });
}

export function canAssignSubjectTask(user: { status: string; roles: string[]; teaching_subjects?: string[]; school_id?: string }, subject: string | undefined, role: "teacher" | "grader", schoolId?: string): boolean {
  // 学科资格不能替代岗位或学校范围；服务端仍检查岗位实际权限和原批阅回避规则。
  return user.status === "active" && user.roles.includes(role)
    && (!schoolId || user.school_id === schoolId) && canTeachSubjects(user, [subject ?? ""]);
}
