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

function isScoringRuleConfigKeyApplicable(type: string, key: string) {
  if (type === "multiple_choice") return ["score_per_correct_option", "wrong_option_penalty", "minimum_score"].includes(key);
  if (type === "numeric") return ["absolute", "relative"].includes(key);
  return false;
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
  const loadedQuestionKey = useRef<string | null>(null);
  const saveInFlight = useRef(false);

  useEffect(() => {
    const questionKey = selectedQuestion?.id ?? null;
    // 同一题的未保存编辑优先保留；换题后重新加载，避免把上题草稿带入新题。
    if ((scoringRuleDirty || preserveUnsaved) && loadedQuestionKey.current === questionKey) return;
    loadedQuestionKey.current = questionKey;
    let active = true;
    async function loadRules() {
      if (!selectedQuestion || !objectiveRuleType) {
        setScoringRules([]);
        setScoringRuleConfig({});
        setScoringRuleDirty(false);
        return;
      }
      try {
        const result = await listScoringRules(selectedQuestion.id);
        if (!active) return;
        setScoringRules(result.scoring_rules);
        const editable = result.scoring_rules.find((rule) => rule.status === "draft")
          ?? result.scoring_rules.find((rule) => rule.status === "published");
        setScoringRuleConfig(editable?.config ?? {});
        setScoringRuleDirty(false);
      } catch (error) {
        if (active) message.error(getUserErrorMessage(error, "操作失败，请稍后重试"));
      }
    }
    void loadRules();
    return () => { active = false; };
  }, [message, objectiveRuleType, preserveUnsaved, scoringRuleDirty, selectedQuestion]);

  const setRuleConfig = (key: string, value: unknown) => {
    setScoringRuleDirty(true);
    setScoringRuleConfig((current) => ({ ...current, [key]: value }));
  };

  const prepareScoringRuleSave = (score = selectedQuestion?.score, type = objectiveRuleType) => {
    if (!type || !score || !Number.isFinite(score) || score <= 0) throw new Error("请先核对本题题型和分值");
    for (const [key, label, maximum] of [
      ["score_per_correct_option", "正确选项分值", score], ["wrong_option_penalty", "错误选项扣分", score], ["minimum_score", "最低得分", score],
      ["absolute", "绝对误差", Number.POSITIVE_INFINITY], ["relative", "相对误差", 1]
    ] as const) {
      if (!isScoringRuleConfigKeyApplicable(type, key)) continue;
      if (scoringRuleConfig[key] === undefined) continue;
      const value = Number(scoringRuleConfig[key]);
      if (!Number.isFinite(value) || value < 0 || value > maximum) throw new Error(`${label}不在本次题目分值允许的范围内，请核对评分配置`);
    }
    return { type, config: structuredClone({ ...scoringRuleConfig, max_score: score }), draft: type === objectiveRuleType ? scoringRules.find((rule) => rule.status === "draft") : undefined };
  };

  const persistScoringRuleSave = async (questionId: string, prepared: ReturnType<typeof prepareScoringRuleSave>, publish: boolean, options: { notify?: boolean; reload?: boolean } = {}) => {
    // state 负责展示保存状态；ref 在首个 await 前阻止同一轮重复写入。
    if (saveInFlight.current) return false;
    saveInFlight.current = true; setSavingScoringRule(true);
    try {
      const draftScoringRule = prepared.draft;
      const saved = draftScoringRule
        ? (await updateScoringRule(draftScoringRule.id, prepared.config, draftScoringRule.revision)).scoring_rule
        : (await createScoringRule(questionId, prepared.type, prepared.config)).scoring_rule;
      // 先保留已保存草稿的版本与修订号；后续发布/读取失败重试时不得再创建另一份草稿。
      setScoringRules(current => [saved, ...current.filter(rule => rule.id !== saved.id)]);
      if (publish) {
        await publishScoringRule(saved.id);
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
      setScoringRules(result.scoring_rules);
      const editable = result.scoring_rules.find((rule) => rule.status === "draft")
        ?? result.scoring_rules.find((rule) => rule.status === "published");
      setScoringRuleConfig(editable?.config ?? {});
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
