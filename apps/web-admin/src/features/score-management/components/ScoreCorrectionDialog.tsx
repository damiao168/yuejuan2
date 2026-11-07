import { Alert, Button, Modal } from "antd";
import type { ScoreRelease } from "../../../api/scoreReleases";
import type { useScoreReleaseWorkflow } from "../hooks/useScoreReleaseWorkflow";

type Workflow = Pick<ReturnType<typeof useScoreReleaseWorkflow>, "correction" | "closeCorrection" | "publishCorrection" | "previewCorrection">;

/**
 * 发布一项已完成的题目级复评。结果版本沿用当前已发布版本的公开设置（匿名范例卷除外），
 * 旧版本留在发布记录中；本弹窗不修改复评结论。
 */
export function ScoreCorrectionDialog({ workflow, currentRelease, remaining, actioning, canWrite }: Readonly<{
  workflow: Workflow;
  /** 页面正在重读或已有提交进行中时为 false；不能依据旧快照提交。 */
  canWrite: boolean;
  currentRelease?: ScoreRelease;
  /** 包括本项在内仍待发布的更正数。 */
  remaining: number;
  actioning: string | null;
}>) {
  const item = workflow.correction;
  const busy = actioning === "release-publish" || actioning === "release-preview";
  return <Modal
    open={Boolean(item)}
    title="发布更正"
    width={560}
    onCancel={busy ? undefined : workflow.closeCorrection}
    maskClosable={!busy}
    footer={[
      <Button key="preview" disabled={busy || !canWrite} loading={actioning === "release-preview"} onClick={workflow.previewCorrection}>查看学生视角</Button>,
      <Button key="cancel" disabled={busy} onClick={workflow.closeCorrection}>取消</Button>,
      <Button key="publish" type="primary" disabled={(busy && actioning !== "release-publish") || (!busy && !canWrite)} loading={actioning === "release-publish"} onClick={workflow.publishCorrection}>发布更正</Button>
    ]}
  >
    {item ? <div className="score-publish-dialog">
      <p><strong>题目复评</strong> · 影响 {item.job.affected_count} 份答卷</p>
      <p>复评原因：{item.job.reason_text || "未填写"}</p>
      <p>发布后学生看到更正后的成绩{currentRelease ? `；原 V${currentRelease.version} 保留在发布记录中` : ""}。公开内容沿用当前版本的设置，不包括匿名范例卷。</p>
      {remaining > 1 ? <Alert type="info" showIcon message={`还有 ${remaining - 1} 项更正待发布`} description="本项发布后，再依次发布其余更正。" /> : null}
    </div> : null}
  </Modal>;
}
