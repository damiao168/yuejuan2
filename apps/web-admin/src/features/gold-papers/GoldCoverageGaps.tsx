import { useCallback, useEffect, useRef, useState } from "react";
import { Alert, Button, List, Spin, Tag } from "antd";
import type { GoldCoverage } from "../../api/goldPapers";
import { getGoldCoverage } from "../../api/goldPapers";
import { listQuestions } from "../../api/papers";
import { goldCoverageGapLabel } from "./goldPaperPresentation";

export function GoldCoverageGaps({ examId }: Readonly<{ examId?: string }>) {
  const [items, setItems] = useState<Array<{ questionNo: string; coverage: GoldCoverage }>>([]);
  const [unavailableCount, setUnavailableCount] = useState(0);
  const [loading, setLoading] = useState(false);
  const [failed, setFailed] = useState(false);

  const request = useRef(0);
  const load = useCallback(async () => {
    const version = ++request.current;
    if (!examId) return;
    setLoading(true); setFailed(false);
    try {
      const questions = (await listQuestions(examId)).questions;
      const results = await Promise.all(questions.map(async (question) => {
        try { return { questionNo: question.question_no, coverage: (await getGoldCoverage(examId, question.id)).coverage }; }
        catch { return null; }
      }));
      // 换考试或卸载后，迟到的覆盖信息不能覆盖当前考试。
      if (version !== request.current) return;
      setUnavailableCount(results.filter((item) => item === null).length);
      // 读取失败的题目单独计为无法判断，不能和没有覆盖缺口的题目混为一谈。
      setItems(results.flatMap((item) => item?.coverage.gaps.length ? [item] : []));
    } catch { if (version === request.current) { setFailed(true); setUnavailableCount(0); } }
    finally { if (version === request.current) setLoading(false); }
  }, [examId]);

  useEffect(() => { void load(); return () => { request.current++; }; }, [load]);
  if (!examId) return null;
  if (loading) return <Spin size="small" />;
  if (failed) return <Alert type="warning" showIcon message="标准卷覆盖暂时无法读取" action={<Button size="small" onClick={() => void load()}>重试</Button>} />;
  if (unavailableCount > 0 && !items.length) return <Alert type="warning" showIcon message={`${unavailableCount} 道题尚无冻结评估快照，无法判断标准卷覆盖`} />;
  if (!items.length) return <Alert type="success" showIcon message="标准卷覆盖暂无阻断缺口" />;
  const unavailableMessage = unavailableCount ? `；另有 ${unavailableCount} 道题无法判断` : "";
  return (
    <Alert
      type="warning"
      showIcon
      message={`${items.length} 道题的标准卷覆盖不完整${unavailableMessage}`}
      description={<List size="small" dataSource={items} renderItem={(item) => <List.Item><strong>{item.questionNo}</strong><span>{item.coverage.gaps.map((gap) => <Tag key={gap}>{goldCoverageGapLabel(gap)}</Tag>)}</span></List.Item>} />}
    />
  );
}
