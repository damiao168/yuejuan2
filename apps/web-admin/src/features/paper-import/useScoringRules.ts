import { useEffect, useRef, useState } from "react";
import { App } from "antd";
import { getUserErrorMessage } from "../../api/client";
import {
  createScoringRule,
  listScoringRules,
  publishScoringRule,
  updateScoringRule,
  type Question
} from "../../api/papers";
import { usesLegacyMultipleChoiceScoring, withMultipleChoiceScoreDefaults } from "./multipleChoiceScoring";

function isScoringRuleConfigKeyApplicable(type: string, key: string, config: Record<string, unknown>) {
  if (type === "multiple_choice") return (usesLegacyMultipleChoiceScoring(config)
    ? ["score_per_correct_option", "wrong_option_penalty", "minimum_score"]
    : ["partial_score", "wrong_answer_score"]).includes(key);
  if (type === "numeric") return ["absolute", "relative"].includes(key);
  return false;
}

function editableScoringRuleConfig(question: Question, type: string, config?: Record<string, unknown>) {
  if (type !== "multiple_choice") return config ?? {};
  const tolerance = question.answer_key?.tolerance;
  // 已有评分规则优先；只有尚无规则的多选题才沿用答案容差中的历史政策。
  const inherited = config ?? (tolerance && typeof tolerance === "object" && !Array.isArray(tolerance) ? tolerance as Record<string, unknown> : {});
  return withMultipleChoiceScoreDefaults(inherited);
}

export function useScoringRules({
  selectedQuestion,
  objectiveRuleType,
  preserveUnsaved = false,
  onChanged
}: {
  selectedQuestion: Question | null;
  objectiveRuleType: string;
  preserveUnsaved?: boolean;
  onChanged?: () => void;
}) {
  const { message } = App.useApp();
  const [scoringRules, setScoringRules] = useState<Awaited<ReturnType<typeof listScoringRules>>["scoring_rules"]>([]);
  const [scoringRuleConfig, setScoringRuleConfig] = useState<Record<string, unknown>>({});
  const [savingScoringRule, setSavingScoringRule] = useState(false);
  const [scoringRuleDirty, setScoringRuleDirty] = useState(false);
  const [scoringRulesLoading, setScoringRulesLoading] = useState(false);
  const [scoringRulesError, setScoringRulesError] = useState<string | null>(null);
  const [rulesRetry, setRulesRetry] = useState(0);
  const loadedQuestionKey = useRef<string | null>(null);
  const currentScopeKey = `${selectedQuestion?.id ?? "create"}:${objectiveRuleType}`;
  const activeScope = useRef(currentScopeKey);
  activeScope.current = currentScopeKey;
  const readableScope = useRef<string | null>(null);
  const saveInFlight = useRef(false);

  useEffect(() => {
    const questionKey = selectedQuestion?.id ?? null;
    // 同一题的未保存编辑优先保留；换题后重新加载，避免把上题草稿带入新题。
    if ((scoringRuleDirty || preserveUnsaved) && loadedQuestionKey.current === questionKey) return;
    loadedQuestionKey.current = questionKey;
    let active = true;
    readableScope.current = null;
    setScoringRulesLoading(Boolean(selectedQuestion && objectiveRuleType));
    setScoringRulesError(null);
    // 换题立即去掉旧规则ID和配置；读取失败不能拿上一题的draft更新当前题。
    setScoringRules([]);
    setScoringRuleConfig({});
    setScoringRuleDirty(false);
    async function loadRules() {
      if (!selectedQuestion || !objectiveRuleType) {
        setScoringRules([]);
        setScoringRuleConfig({});
        setScoringRuleDirty(false);
        readableScope.current = currentScopeKey;
        return;
      }
      try {
        const result = await listScoringRules(selectedQuestion.id);
        if (!active || activeScope.current !== currentScopeKey) return;
        if (result.scoring_rules.some(rule => rule.question_id !== selectedQuestion.id)) throw new Error("评分规则与当前题目不一致，请重新读取");
        setScoringRules(result.scoring_rules);
        const editable = result.scoring_rules.find((rule) => rule.status === "draft")
          ?? result.scoring_rules.find((rule) => rule.status === "published");
        setScoringRuleConfig(editableScoringRuleConfig(selectedQuestion, objectiveRuleType, editable?.config));
        setScoringRuleDirty(false);
        readableScope.current = currentScopeKey;
      } catch (error) {
        if (active && activeScope.current === currentScopeKey) {
          const errorText = getUserErrorMessage(error, "评分规则读取失败，请重新读取后保存");
          setScoringRulesError(errorText);
          message.error(errorText);
        }
      } finally {
        if (active && activeScope.current === currentScopeKey) setScoringRulesLoading(false);
      }
    }
    void loadRules();
    return () => { active = false; };
  }, [message, objectiveRuleType, preserveUnsaved, scoringRuleDirty, selectedQuestion, currentScopeKey, rulesRetry]);

  const setRuleConfig = (key: string, value: unknown) => {
    if (readableScope.current !== currentScopeKey || saveInFlight.current) return;
    setScoringRuleDirty(true);
    setScoringRuleConfig((current) => ({ ...current, [key]: value }));
  };

  const prepareScoringRuleSave = (score = selectedQuestion?.score, type = objectiveRuleType) => {
    if (readableScope.current !== currentScopeKey || activeScope.current !== currentScopeKey) throw new Error("请先成功读取当前题目的评分规则，再保存修改");
    if (!type || !score || !Number.isFinite(score) || score <= 0) throw new Error("请先核对本题题型和分值");
    const config = type === "multiple_choice" ? withMultipleChoiceScoreDefaults(scoringRuleConfig) : scoringRuleConfig;
    for (const [key, label, maximum] of [
      ["partial_score", "少选且无错选得分", score], ["wrong_answer_score", "有错选得分", score],
      ["score_per_correct_option", "正确选项分值", score], ["wrong_option_penalty", "错误选项扣分", score], ["minimum_score", "最低得分", score],
      ["absolute", "绝对误差", Number.POSITIVE_INFINITY], ["relative", "相对误差", 1]
    ] as const) {
      if (!isScoringRuleConfigKeyApplicable(type, key, config)) continue;
      if (config[key] === undefined) continue;
      const value = Number(config[key]);
      const invalidFixedScore = (key === "partial_score" || key === "wrong_answer_score") && typeof config[key] !== "number";
      if (invalidFixedScore || !Number.isFinite(value) || value < 0 || value > maximum) throw new Error(`${label}不在本次题目分值允许的范围内，请核对评分配置`);
    }
    return { type, questionId: selectedQuestion?.id ?? null, scopeKey: currentScopeKey, config: structuredClone({ ...config, max_score: score }), draft: type === objectiveRuleType ? scoringRules.find((rule) => rule.status === "draft") : undefined };
  };

  const persistScoringRuleSave = async (questionId: string, prepared: ReturnType<typeof prepareScoringRuleSave>, publish: boolean, options: { notify?: boolean; reload?: boolean } = {}) => {
    // state 负责展示保存状态；ref 在首个 await 前阻止同一轮重复写入。
    if (saveInFlight.current) return false;
    if (activeScope.current !== prepared.scopeKey || readableScope.current !== prepared.scopeKey || (prepared.questionId && prepared.questionId !== questionId) || (prepared.draft && prepared.draft.question_id !== questionId)) throw new Error("评分规则所属题目已变化，请重新读取当前题目后保存");
    const isCurrent = () => activeScope.current === prepared.scopeKey;
    saveInFlight.current = true; setSavingScoringRule(true);
    try {
      const draftScoringRule = prepared.draft;
      const saved = draftScoringRule
        ? (await updateScoringRule(draftScoringRule.id, prepared.config, draftScoringRule.revision)).scoring_rule
        : (await createScoringRule(questionId, prepared.type, prepared.config)).scoring_rule;
      if (!isCurrent()) return false;
      if (saved.question_id !== questionId) throw new Error("评分规则保存回执与当前题目不一致");
      // 先保留已保存草稿的版本与修订号；后续发布/读取失败重试时不得再创建另一份草稿。
      setScoringRules(current => [saved, ...current.filter(rule => rule.id !== saved.id)]);
      if (publish) {
        await publishScoringRule(saved.id);
        if (!isCurrent()) return false;
        if (options.notify !== false) message.success("评分规则已发布并锁定版本");
      } else if (options.notify !== false) {
        message.success("评分规则草稿已保存");
      }
      if (options.reload === false && !publish) {
        // 整题保存已取得写入回执，无需把随后读取失败误报为未保存；最终统一刷新负责读取。
        setScoringRuleConfig(prepared.config); setScoringRuleDirty(false);
        return true;
      }
      const result = await listScoringRules(questionId);
      if (!isCurrent()) return false;
      if (result.scoring_rules.some(rule => rule.question_id !== questionId)) throw new Error("评分规则读取回执与当前题目不一致");
      setScoringRules(result.scoring_rules);
      const editable = result.scoring_rules.find((rule) => rule.status === "draft")
        ?? result.scoring_rules.find((rule) => rule.status === "published");
      setScoringRuleConfig(selectedQuestion ? editableScoringRuleConfig(selectedQuestion, prepared.type, editable?.config) : editable?.config ?? {});
      setScoringRuleDirty(false);
      if (options.notify !== false) onChanged?.();
      return true;
    } finally {
      // 写入、发布或读取失败都释放本次保存锁，原异常仍交给调用方处理。
      saveInFlight.current = false; setSavingScoringRule(false);
    }
  };
  const saveScoringRule = async (publish: boolean) => {
    try {
      if (!selectedQuestion || !objectiveRuleType) return false;
      return await persistScoringRuleSave(selectedQuestion.id, prepareScoringRuleSave(), publish);
    } catch (error) { message.error(getUserErrorMessage(error, "评分规则暂不可保存")); return false; }
  };

  return {
    scoringRules,
    scoringRuleConfig,
    scoringRuleDirty,
    scoringRulesLoading,
    scoringRulesError,
    scoringRulesReadable: readableScope.current === currentScopeKey,
    reloadScoringRules: () => setRulesRetry(value => value + 1),
    discardScoringRuleDraft: () => setScoringRuleDirty(false),
    savingScoringRule,
    draftScoringRule: scoringRules.find((rule) => rule.status === "draft"),
    publishedScoringRule: scoringRules.find((rule) => rule.status === "published"),
    setRuleConfig,
    saveScoringRule,
    prepareScoringRuleSave,
    persistScoringRuleSave
  };
}
