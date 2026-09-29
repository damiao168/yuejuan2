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

export function useScoringRules({
  selectedQuestion,
  objectiveRuleType,
  onChanged
}: {
  selectedQuestion: Question | null;
  objectiveRuleType: string;
  onChanged?: () => void;
}) {
  const { message } = App.useApp();
  const [scoringRules, setScoringRules] = useState<Awaited<ReturnType<typeof listScoringRules>>["scoring_rules"]>([]);
  const [scoringRuleConfig, setScoringRuleConfig] = useState<Record<string, unknown>>({});
  const [savingScoringRule, setSavingScoringRule] = useState(false);
  const [scoringRuleDirty, setScoringRuleDirty] = useState(false);
  const loadedQuestionKey = useRef<string | null>(null);

  useEffect(() => {
    const questionKey = selectedQuestion?.id ?? null;
    // 同一题的未保存编辑优先保留；换题后重新加载，避免把上题草稿带入新题。
    if (scoringRuleDirty && loadedQuestionKey.current === questionKey) return;
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
  }, [message, objectiveRuleType, scoringRuleDirty, selectedQuestion]);

  const setRuleConfig = (key: string, value: unknown) => {
    setScoringRuleDirty(true);
    setScoringRuleConfig((current) => ({ ...current, [key]: value }));
  };

  const saveScoringRule = async (publish: boolean) => {
    if (!selectedQuestion || !objectiveRuleType) return;
    setSavingScoringRule(true);
    try {
      const draftScoringRule = scoringRules.find((rule) => rule.status === "draft");
      const saved = draftScoringRule
        ? (await updateScoringRule(draftScoringRule.id, scoringRuleConfig, draftScoringRule.revision)).scoring_rule
        : (await createScoringRule(selectedQuestion.id, objectiveRuleType, scoringRuleConfig)).scoring_rule;
      if (publish) {
        await publishScoringRule(saved.id);
        message.success("评分规则已发布并锁定版本");
      } else {
        message.success("评分规则草稿已保存");
      }
      const result = await listScoringRules(selectedQuestion.id);
      setScoringRules(result.scoring_rules);
      const editable = result.scoring_rules.find((rule) => rule.status === "draft")
        ?? result.scoring_rules.find((rule) => rule.status === "published");
      setScoringRuleConfig(editable?.config ?? {});
      setScoringRuleDirty(false);
      onChanged?.();
      return true;
    } catch (error) {
      message.error(getUserErrorMessage(error, "操作失败，请稍后重试"));
      return false;
    } finally {
      setSavingScoringRule(false);
    }
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
    saveScoringRule
  };
}
