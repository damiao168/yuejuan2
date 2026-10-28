import { useEffect, useState } from "react";
import { Button, Pagination, Space } from "antd";
import type { QuestionBankSearchPage } from "@edugrade/sdk";
import { questionBankApi } from "../../api/questionBank";
import { getUserErrorMessage } from "../../api/client";
import { hasEveryPermission, type SessionUser } from "../../auth/session";
import { ErrorState } from "../../components/PageState";

export function QuestionBankHandoffs({ user, onNavigate }: Readonly<{ user: SessionUser; onNavigate: (path: string) => void }>) {
  const canReview = hasEveryPermission(user, ["question_bank:read", "question_bank:review"]);
  const canPublish = hasEveryPermission(user, ["question_bank:read", "question_bank:publish"]);
  const [action, setAction] = useState<"review" | "publish">(canReview ? "review" : "publish");
  const [offset, setOffset] = useState(0);
  const [refresh, setRefresh] = useState(0);
  // 已经显示的结果也绑定账号与查询，切换身份的首次渲染不能暂留前一个人的题目。
  const queryOwner = JSON.stringify([user.id, user.tenant, action, offset]);
  const [ownedPage, setOwnedPage] = useState<{ owner: string; page: QuestionBankSearchPage }>();
  const page = ownedPage?.owner === queryOwner ? ownedPage.page : undefined;
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);
  // biome-ignore lint/correctness/useExhaustiveDependencies: 账号变化和显式刷新必须废弃旧请求，即使当前筛选条件未改变。
  useEffect(() => {
    setOwnedPage(undefined); setError("");
    if (action === "review" ? !canReview : !canPublish) {
      setLoading(false);
      if (canReview || canPublish) { setAction(canReview ? "review" : "publish"); setOffset(0); }
      return;
    }
    const controller = new AbortController();
    setLoading(true);
    // 待办沿用实时职责和独立审核约束；前一账号或页码的回执不能回填当前工作台。
    void questionBankApi.searchQuestionBankItems({ query: { pending_action: action, limit: 5, offset }, signal: controller.signal })
      .then(result => { if (!controller.signal.aborted) setOwnedPage({ owner: queryOwner, page: result }); })
      .catch(cause => { if (!controller.signal.aborted) setError(getUserErrorMessage(cause, "题库待办读取失败")); })
      .finally(() => { if (!controller.signal.aborted) setLoading(false); });
    return () => controller.abort();
  }, [user.id, user.tenant, action, offset, refresh, canReview, canPublish, queryOwner]);
  if (!canReview && !canPublish) return null;
  return <section aria-label="题库待办"><h2>题库待办</h2><Space wrap>
    {canReview ? <Button type={action === "review" ? "primary" : "default"} onClick={() => { setOffset(0); setAction("review"); }}>待我审核</Button> : null}
    {canPublish ? <Button type={action === "publish" ? "primary" : "default"} onClick={() => { setOffset(0); setAction("publish"); }}>待我发布</Button> : null}
    <Button loading={loading} onClick={() => setRefresh(value => value + 1)}>刷新题库待办</Button>
  </Space>
    {error ? <ErrorState message={error} onRetry={() => setRefresh(value => value + 1)} /> : <>
      {!loading && page && !page.total ? <p>当前没有{action === "review" ? "待审核" : "待发布"}题目。</p> : null}
      <ul>{page?.items.map(entry => <li key={entry.version.id}><Button type="link" onClick={() => onNavigate(`/question-bank?bank=${encodeURIComponent(entry.item.bank_id)}&item=${encodeURIComponent(entry.item.id)}&version=${encodeURIComponent(entry.version.id)}`)}>{entry.version.stem.slice(0, 120) || entry.item.item_code}</Button> · {entry.version.handoff?.claimed_by ? "已领取" : (action === "review" ? entry.version.handoff?.reviewer_id : entry.version.handoff?.publisher_id) ? "已指派给我" : "待领取"}</li>)}</ul>
      {(page?.total ?? 0) > 5 ? <Pagination current={offset / 5 + 1} total={page?.total} pageSize={5} showSizeChanger={false} onChange={value => setOffset((value - 1) * 5)} /> : null}
    </>}
  </section>;
}
