/*!
 * Adapted from deepseek-ai/deepseek-harness token-format.ts, formatTokens.
 * Copyright (c) 2026 DeepSeek. MIT License.
 * Source: https://github.com/deepseek-ai/deepseek-harness/blob/39163dd30f703a79079e2d2446545cff3c453738/packages/client/ui-chat/src/client/chat/token-format.ts
 * License: ../../../public/licenses/deepseek-harness-LICENSE.txt
 * 将上游本地化回调替换为固定 K/M 单位；调用方先校验非负 Token 计数。
 */
export function formatConversationTokens(value: number): string {
  const scaled = (candidate: number): string =>
    candidate >= 100 ? String(Math.round(candidate)) : String(Math.round(candidate * 10) / 10);
  if (value < 1_000) return String(value);
  if (value < 1_000_000) return `${scaled(value / 1_000)}K`;
  return `${scaled(value / 1_000_000)}M`;
}
