export const numberText = (value: number) => new Intl.NumberFormat("zh-CN").format(value ?? 0);

export function tokenText(value: number) {
  if (!Number.isFinite(value)) return "—";
  if (value >= 1_000_000) return `${(value / 1_000_000).toFixed(1)}M`;
  if (value >= 1_000) return `${(value / 1_000).toFixed(1)}K`;
  return numberText(value);
}

export function fullDate(value?: string) {
  if (!value) return "—";
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? "—" : new Intl.DateTimeFormat("zh-CN", { dateStyle: "medium", timeStyle: "short" }).format(date);
}

export function shortDate(value?: string) {
  if (!value) return "—";
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? "—" : date.toLocaleDateString("sv-SE");
}

export function relativeTime(value?: string) {
  if (!value) return "从未活跃";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "—";
  const minutes = Math.max(0, Math.floor((Date.now() - date.getTime()) / 60_000));
  if (minutes < 1) return "刚刚";
  if (minutes < 60) return `${minutes} 分钟前`;
  if (minutes < 1_440) return `${Math.floor(minutes / 60)} 小时前`;
  if (minutes < 2_880) return "昨天";
  return `${Math.floor(minutes / 1_440)} 天前`;
}

export const roleLabel: Record<string, string> = {
  school_admin: "学校管理员", tenant_admin: "租户管理员", teacher: "教师", grader: "阅卷员", arbitrator: "仲裁员", auditor: "审计员", student: "学生"
};

export const featureLabel: Record<string, string> = {
  paper_import: "试卷识别", school_ai_chat: "AI 助手", subjective_grading: "主观题阅卷", model_probe: "模型检测", model_evaluation: "模型评估", other: "其他"
};
