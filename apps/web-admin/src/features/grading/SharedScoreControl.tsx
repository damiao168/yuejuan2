import { Button, Checkbox, InputNumber, Space } from "antd";
import { RubricCriterion } from "@edugrade/ui";
import type { RubricPoint } from "../../api/papers";
import { StatusTag } from "../../components/StatusTag";

export interface SharedScoreValue {
  score: number | null;
  rubricSelections: Record<string, number>;
}

export function SharedScoreControl({
  value,
  maxScore,
  rubricPoints,
  disabled = false,
  onChange
}: Readonly<{
  value: SharedScoreValue;
  maxScore: number;
  rubricPoints: RubricPoint[];
  disabled?: boolean;
  onChange: (value: SharedScoreValue) => void;
}>) {
  const rubricTotal = Number(rubricPoints.reduce((total, point) => total + (value.rubricSelections[point.id] ?? 0), 0).toFixed(1));
  const setPoint = (point: RubricPoint, score: number) => {
    // 调整采分点后重新合计最终分；直接填写最终分产生的人工调整不继续沿用。
    const rubricSelections = { ...value.rubricSelections, [point.id]: score };
    onChange({ ...value, rubricSelections, score: Number(rubricPoints.reduce((total, item) => total + (rubricSelections[item.id] ?? 0), 0).toFixed(1)) });
  };

  return (
    <>
      <div className="score-input-row">
        <InputNumber
          aria-label="最终得分"
          disabled={disabled}
          min={0}
          max={maxScore}
          precision={1}
          value={value.score}
          placeholder="最终分"
          onChange={(score) => onChange({ ...value, score: score === null ? null : Number(score) })}
        />
        <span>/ {maxScore || "-"}</span>
      </div>

      {rubricPoints.length > 0 ? (
        <div className="rubric-score-list">
          <div className="rubric-score-head">
            <strong>评分细则</strong>
            <Space size={8} wrap>
              <span>{rubricTotal} 分</span>
              {value.score !== null && value.score !== rubricTotal
                ? <StatusTag tone="warning">{`人工调整：与细则合计相差 ${Number((value.score - rubricTotal).toFixed(1))} 分`}</StatusTag>
                : null}
              <Button size="small" disabled={disabled} onClick={() => onChange({ ...value, score: rubricTotal })}>
                填入最终分
              </Button>
            </Space>
          </div>
          <p className="grading-inline-note">调整评分点会自动合计最终分；直接修改最终分属于人工调整，请记录原因。</p>
          {rubricPoints.map((point, index) => (
            <RubricCriterion
              key={point.id}
              label={`${index + 1}. ${point.description || "未命名采分点"}`}
              score={point.score}
              required={point.required}
              leading={
                <Checkbox
                  disabled={disabled}
                  checked={(value.rubricSelections[point.id] ?? 0) > 0}
                  onChange={(event) => setPoint(point, event.target.checked ? point.score : 0)}
                />
              }
            >
              <InputNumber
                aria-label={`${point.description || "采分点"}得分`}
                disabled={disabled}
                min={0}
                max={point.score}
                precision={1}
                value={value.rubricSelections[point.id] ?? 0}
                onChange={(score) => setPoint(point, Number(score ?? 0))}
              />
            </RubricCriterion>
          ))}
        </div>
      ) : <p className="grading-inline-note">当前题目没有评分细则，请直接填写最终得分。</p>}
    </>
  );
}
