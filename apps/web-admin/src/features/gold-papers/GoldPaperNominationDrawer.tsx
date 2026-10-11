import { useEffect, useMemo, useState } from "react";
import { Alert, App, Button, Descriptions, Drawer, Input, InputNumber, List, Select, Space, Tag } from "antd";
import { nominateGoldPaper } from "../../api/goldPapers";
import { getUserErrorMessage } from "../../api/client";
import { MathMarkdown } from "../../components/MathMarkdown";
import type { GoldRubricPoint } from "./goldPaperPresentation";
import { buildRubricEvidence } from "./goldPaperPresentation";

export interface GoldPaperNominationCandidate {
  examId: string;
  questionId: string;
  questionNo: string;
  submissionId: string;
  answerImageUrl: string;
  referenceScore: number;
  maxScore: number;
  explanation: string;
  sourceGradeId: string;
  rubricPoints: GoldRubricPoint[];
  rubricSelections: Record<string, number>;
}

export function GoldPaperNominationDrawer({
  candidate,
  onClose,
  onCreated
}: Readonly<{
  candidate: GoldPaperNominationCandidate | null;
  onClose: () => void;
  onCreated?: () => void;
}>) {
  const { message } = App.useApp();
  const [score, setScore] = useState<number | null>(null);
  const [explanation, setExplanation] = useState("");
  const [errorTags, setErrorTags] = useState<string[]>([]);
  const [submitting, setSubmitting] = useState(false);
  const rubricEvidence = useMemo(
    () => candidate ? buildRubricEvidence(candidate.rubricSelections, candidate.rubricPoints) : {},
    [candidate]
  );

  useEffect(() => {
    setScore(candidate?.referenceScore ?? null);
    setExplanation(candidate?.explanation ?? "");
    setErrorTags([]);
  }, [candidate]);

  const submit = async () => {
    if (!candidate || score === null || score < 0 || score > candidate.maxScore) {
      message.error("拟定分必须在题目分值范围内");
      return;
    }
    if (!explanation.trim()) {
      message.error("请说明这份答卷代表的评分边界和判分依据");
      return;
    }
    setSubmitting(true);
    try {
      // 提名沿用已提交评分的来源 ID 和冻结采分点证据，由后续审批决定是否成为标准卷。
      await nominateGoldPaper(candidate.examId, candidate.questionId, {
        submission_id: candidate.submissionId,
        reference_score: score,
        explanation: explanation.trim(),
        trait_scores: rubricEvidence,
        error_tags: errorTags,
        source_grade_ids: [candidate.sourceGradeId]
      });
      message.success("已提名为标准卷，等待主阅审批");
      onCreated?.();
      onClose();
    } catch (error) {
      message.error(getUserErrorMessage(error, "标准卷提名失败"));
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <Drawer title="提名为标准卷" width={720} open={Boolean(candidate)} onClose={onClose} destroyOnHidden>
      {candidate ? (
        <div className="gold-paper-form">
          <Alert
            type="info"
            showIcon
            message={`${candidate.questionNo} · 真实答卷`}
            description="标准卷会引用已提交的人工评分事实，并冻结当前 Rubric；审批前不会进入校准或模型评测。"
          />
          <img className="gold-paper-answer" src={candidate.answerImageUrl} alt={`${candidate.questionNo} 答题区域`} />
          <Descriptions size="small" column={2} bordered>
            <Descriptions.Item label="拟定分">
              <Space><InputNumber min={0} max={candidate.maxScore} value={score} onChange={(value) => setScore(value === null ? null : Number(value))} /><span>/ {candidate.maxScore}</span></Space>
            </Descriptions.Item>
            <Descriptions.Item label="评分事实">1 条已提交人工评分</Descriptions.Item>
          </Descriptions>
          <section>
            <h3>Rubric 采分点证据</h3>
            <List
              size="small"
              bordered
              locale={{ emptyText: "本题冻结 Rubric 未定义采分点，以解释和已提交评分事实为证据" }}
              dataSource={candidate.rubricPoints}
              renderItem={(point) => (
                <List.Item extra={<Tag color={(rubricEvidence[point.id] ?? 0) > 0 ? "green" : "default"}>{rubricEvidence[point.id] ?? 0} / {point.score} 分</Tag>}>
                  <MathMarkdown promoteInlineMath={false}>{point.description}</MathMarkdown>
                </List.Item>
              )}
            />
          </section>
          <label className="gold-paper-field">
            <span>评分边界与判分依据</span>
            <Input.TextArea rows={4} value={explanation} onChange={(event) => setExplanation(event.target.value)} placeholder="说明命中了哪些采分点、缺失了什么、为什么代表这一分数边界" />
          </label>
          <label className="gold-paper-field">
            <span>典型错误标签（可选）</span>
            <Select mode="tags" value={errorTags} onChange={setErrorTags} placeholder="例如 unit_error、missing_key_step" tokenSeparators={[","]} />
          </label>
          <Space>
            <Button type="primary" loading={submitting} onClick={() => void submit()}>提交提名</Button>
            <Button onClick={onClose}>取消</Button>
          </Space>
        </div>
      ) : null}
    </Drawer>
  );
}
