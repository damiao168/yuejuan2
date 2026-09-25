export const preparationSteps = [
  { key: "students", label: "学生范围", route: "students", action: "设置范围" },
  { key: "paper", label: "考试资料", route: "paper", action: "上传资料" },
  { key: "questions", label: "小题与分值", route: "questions", action: "核对题目" },
  { key: "template", label: "答题卡设置", route: "template", action: "设置答题卡" }
] as const;
