// 规则命中仅表示可疑并触发复核；未命中不代表已穷尽提示词注入方式。
const DEFAULT_RULES = [
  { id: "ignore-rubric-cn", pattern: /忽略(?:之前|以上|所有)?(?:的)?(?:评分|规则|标准|指令)/i },
  { id: "full-score-cn", pattern: /(?:给我|直接给|应该给)(?:满分|\d+(?:\.\d+)?分)/i },
  { id: "role-override-cn", pattern: /你现在是(?:老师|教师|系统|管理员|开发者)/i },
  { id: "schema-bypass-cn", pattern: /(?:输出.*schema.*之外|不要告诉老师)/i },
  { id: "ignore-rubric-en", pattern: /ignore(?:all|previous|the)?(?:rules|instructions|rubric|gradingcriteria)/i },
  { id: "full-score-en", pattern: /givemefull(?:marks|score|credit)/i },
  { id: "role-override-en", pattern: /youarenow(?:the)?(?:teacher|system|admin|developer)/i }
];

export function normalizeUntrustedInstructionText(text) {
  return String(text ?? "")
    .normalize("NFKC")
    .replace(/[\u200B-\u200D\uFEFF]/g, "")
    .toLowerCase()
    .replace(/\s+/g, "");
}

export class PromptInjectionDetector {
  constructor(rules = DEFAULT_RULES) {
    this.rules = rules;
  }

  detect(text) {
    const normalized = normalizeUntrustedInstructionText(text);
    const matches = this.rules.filter((rule) => rule.pattern.test(normalized)).map((rule) => rule.id);
    return {
      detected: matches.length > 0,
      matches,
      risk_flags: matches.length > 0 ? ["PROMPT_INJECTION_SUSPECTED", "HUMAN_REVIEW_REQUIRED"] : []
    };
  }
}

export function detectPromptInjection(text) {
  return new PromptInjectionDetector().detect(text);
}
