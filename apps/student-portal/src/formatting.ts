// 学生端展示格式；只格式化已取得的值，不计算成绩或改变发布语义。


export function formatScore(score: number) { return Number.isInteger(score) ? String(score) : score.toFixed(1); }

export function formatDate(value: string) { return new Intl.DateTimeFormat("zh-CN", { year: "numeric", month: "long", day: "numeric" }).format(new Date(value)); }

export function formatDateTime(value: string) { return new Intl.DateTimeFormat("zh-CN", { year: "numeric", month: "numeric", day: "numeric", hour: "2-digit", minute: "2-digit", hour12: false }).format(new Date(value)); }

export function questionTypeLabel(type: string) {
  return ({ single_choice: "单选题", multiple_choice: "多选题", true_false: "判断题", fill_blank: "填空题", numeric: "数值题", formula: "公式题", short_answer: "简答题", calculation: "计算题", essay: "解答题", discussion: "论述题", coding: "编程题" } as Record<string, string>)[type] ?? type;
}
