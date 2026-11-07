// 规则命中仅表示可疑并触发复核；未命中不代表已穷尽提示词注入方式。
import { promptInjectionRules } from "./promptInjectionRules.generated.js";

const DEFAULT_RULES = promptInjectionRules.map(rule => ({
  id: rule.id.replaceAll("_", "-"), pattern: new RegExp(rule.pattern, "i"), orderedTerms: rule.ordered_terms
}));

function containsOrderedTerms(text, terms) {
  if (!terms?.length) return false;
  let offset = 0;
  for (const term of terms) {
    const position = text.indexOf(term, offset);
    if (position < 0) return false;
    offset = position + term.length;
  }
  return true;
}

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
    const matches = this.rules.filter((rule) => rule.pattern.test(normalized) || containsOrderedTerms(normalized, rule.orderedTerms)).map((rule) => rule.id);
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
