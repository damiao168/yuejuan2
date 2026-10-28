import { useEffect, useRef, useState } from "react";
import { Alert, Button, Select } from "antd";
import type { AIHumanDisagreement, ReviewTask } from "@edugrade/sdk";
import { getUserErrorMessage } from "../../api/client";
import { listDisagreementReviewTasks } from "../../api/scoringAssurance";

export function DisagreementTaskSelector({ tenantID, disagreement, disabled, onSelect }: Readonly<{
  tenantID: string;
  disagreement: AIHumanDisagreement;
  disabled: boolean;
  onSelect: (task: ReviewTask | undefined) => void;
}>) {
  const [tasks, setTasks] = useState<ReviewTask[]>([]);
  const [selectedID, setSelectedID] = useState<string>();
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [cursor, setCursor] = useState("");
  const request = useRef(0);
  const current = useRef({ tenantID, id: disagreement.id });
  current.current = { tenantID, id: disagreement.id };
  const load = async (nextCursor = "") => {
    const sequence = ++request.current;
    const owner = current.current;
    setSelectedID(undefined); onSelect(undefined);
    setLoading(true); setError("");
    try {
      const response = await listDisagreementReviewTasks(owner.tenantID, disagreement, nextCursor);
      if (sequence !== request.current || current.current.tenantID !== owner.tenantID || current.current.id !== owner.id) return;
      setTasks(previous => nextCursor ? [...previous, ...response.tasks.filter(task => !previous.some(item => item.id === task.id))] : response.tasks);
      setCursor(response.has_more ? response.next_cursor ?? "" : "");
    } catch (cause) {
      if (sequence === request.current) setError(getUserErrorMessage(cause, "任务目录暂不可用；请由该校有阅卷权限的管理员核对后重试。"));
    } finally { if (sequence === request.current) setLoading(false); }
  };
  // 请求固定于本次学校和分歧；本地选择回调改变不能重置正在核对的任务。
  // biome-ignore lint/correctness/useExhaustiveDependencies: 校验范围由学校和分歧身份决定。
  useEffect(() => {
    setTasks([]); setSelectedID(undefined); setCursor(""); onSelect(undefined); void load();
    return () => { request.current += 1; };
  }, [tenantID, disagreement.id]);
  const selected = tasks.find(task => task.id === selectedID);
  return <section>
    <p>只显示当前账号可读且属于同一道题、同一份答卷的任务；没有权限时不会扩大检索范围。</p>
    <Select aria-label="关联人工阅卷任务" showSearch optionFilterProp="label" placeholder="按题号或匿名号选择任务" value={selectedID} loading={loading} disabled={disabled || loading || Boolean(error)} options={tasks.map(task => ({ value: task.id, label: `${task.exam_name || "本次考试"} · 第${task.question_no}题 · 匿名号${task.anonymous_code} · ${task.assigned_to ? "已分配" : "待分配"}` }))} onChange={id => { setSelectedID(id); onSelect(tasks.find(task => task.id === id)); }} />
    {selected ? <p role="status">已核对：第 {selected.question_no} 题 · 匿名号 {selected.anonymous_code} · {selected.assigned_to ? "已有阅卷人" : "尚未分配阅卷人"}。关联只记录处理去向，不提交改分。</p> : null}
    {error ? <Alert type="error" showIcon message={error} action={<Button disabled={disabled} onClick={() => void load()}>重试任务目录</Button>} /> : !loading && !tasks.length ? <p>当前授权范围内暂无匹配任务，请由学校考试负责人核对正式任务后再关联。</p> : null}
    {cursor ? <Button disabled={disabled || loading} onClick={() => void load(cursor)}>继续查找本题任务</Button> : null}
  </section>;
}
