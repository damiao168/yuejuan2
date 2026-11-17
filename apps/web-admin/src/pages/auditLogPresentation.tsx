import { Alert, Descriptions, Drawer } from "antd";
import { FileWarning } from "lucide-react";
import { type AuditLog } from "../api/audit";
import { ApiClientError, getUserErrorMessage } from "../api/client";
import { EmptyState } from "../components/PageState";


export interface AuditLogDetailDrawerProps {
  selectedLog: AuditLog | null;
  open: boolean;
  onClose: () => void;
  actorName: (actor?: string) => string;
}

export const sensitiveKeys = ["password", "token", "authorization", "secret", "credential", "access_token", "refresh_token", "password_hash"];

export function formatError(error: unknown) {
  if (error instanceof ApiClientError) {
    console.warn("操作记录请求失败", error.status, error.code);
    return getUserErrorMessage(error, "操作失败，请稍后重试");
  }
  return getUserErrorMessage(error, "操作失败，请稍后重试");
}

export function formatTime(value?: string) {
  if (!value) {
    return "-";
  }
  const parsed = new Date(value);
  return Number.isNaN(parsed.getTime()) ? value : parsed.toLocaleString("zh-CN", { hour12: false });
}

export function saveBlob(blob: Blob, filename: string) {
  const url = URL.createObjectURL(blob);
  const link = document.createElement("a");
  link.href = url;
  link.download = filename;
  document.body.appendChild(link);
  link.click();
  link.remove();
  URL.revokeObjectURL(url);
}

export function isSensitiveKey(key: string) {
  const normalized = key.toLowerCase();
  return sensitiveKeys.some((item) => normalized.includes(item));
}

// 展示前递归遮盖已知敏感键和常见凭据格式；这是界面兜底，不能替代服务端日志脱敏。
export function maskSensitive(value: unknown, key = ""): unknown {
  if (isSensitiveKey(key)) {
    return "***";
  }
  if (Array.isArray(value)) {
    return value.map((item) => maskSensitive(item));
  }
  if (value && typeof value === "object") {
    return Object.fromEntries(Object.entries(value as Record<string, unknown>).map(([entryKey, entryValue]) => [entryKey, maskSensitive(entryValue, entryKey)]));
  }
  if (typeof value === "string" && /(bearer\s+|password=|token=)/i.test(value)) {
    return value.replace(/(bearer\s+)[^\s]+/i, "$1***").replace(/(password|token)=([^&\s]+)/gi, "$1=***");
  }
  return value;
}

export function maskedJSON(value?: Record<string, unknown>) {
  if (!value || Object.keys(value).length === 0) {
    return "未记录";
  }
  return JSON.stringify(maskSensitive(value), null, 2);
}

export function maskText(value?: string) {
  if (!value) {
    return "-";
  }
  return String(maskSensitive(value));
}

export function actionTone(action: string) {
  if (action.includes("failed") || action.includes("rejected")) {
    return "danger" as const;
  }
  if (action.includes("export") || action.includes("publish") || action.includes("adjusted")) {
    return "warning" as const;
  }
  if (action.includes("created") || action.includes("succeeded") || action.includes("confirmed")) {
    return "success" as const;
  }
  return "processing" as const;
}

export const targetTypeLabels: Record<string, string> = {
  exam: "考试",
  submission: "答卷",
  submission_page: "答卷页",
  answer_segment: "作答区域",
  question: "题目",
  paper: "试卷",
  paper_template: "答题卡模板",
  rubric: "评分细则",
  review_task: "阅卷任务",
  arbitration_task: "仲裁任务",
  double_mark_session: "双评",
  appeal: "申诉",
  scoring_run: "评分批次",
  ocr_task: "识别任务",
  agent_worker_task: "自动处理任务",
  ai_grade: "智能评分",
  final_grade: "最终成绩",
  deduction_point: "扣分点",
  score: "成绩",
  report: "学情报告",
  file: "文件",
  audit_log: "操作记录",
  user: "用户",
  student: "学生",
  school: "机构",
  grade: "年级",
  class: "班级",
  tenant: "机构"
};

export function targetTypeLabel(targetType?: string) {
  if (!targetType) {
    return "系统";
  }
  return targetTypeLabels[targetType] ?? "其他对象";
}

export function describeTarget(record: AuditLog) {
  const typeLabel = targetTypeLabel(record.target_type);
  return record.target_id ? `${typeLabel}（编号 ${record.target_id.slice(0, 8)}）` : typeLabel;
}

export const actionLabels: Record<string, string> = {
  "auth.login_succeeded": "登录成功",
  "auth.login_failed": "登录失败",
  "auth.login_rate_limited": "登录尝试过于频繁被拦截",
  "auth.logout": "退出登录",
  "auth.user_created": "创建用户",
  "user.updated": "更新用户信息",
  "org.tenant_created": "开通机构",
  "org.tenant_updated": "更新机构信息",
  "org.school_created": "创建机构",
  "org.grade_created": "创建年级",
  "org.class_created": "创建班级",
  "org.student_created": "添加学生",
  "org.student_updated": "更新学生信息",
  "org.students_imported": "导入学生",
  "org.teacher_bound_to_class": "设置教师任教班级",
  "exam.created": "创建考试",
  "exam.updated": "更新考试",
  "exam.status_changed": "变更考试阶段",
  "exam.readiness_confirmed": "确认考试准备就绪",
  "exam.collection_started": "开始采集答卷",
  "exam.archived": "归档考试",
	"exam.answer_sheet_template_bound": "绑定本场答题卡模板",
	"exam.answer_sheet_template_unbound": "解除本场答题卡模板",
	"page.template_matched": "自动识别答题卡模板",
	"page.template_match_failed": "答题卡模板识别失败",
  "paper.created": "创建试卷",
  "paper.config_validated": "校验试卷配置",
  "paper.template_created": "创建答题卡模板",
  "paper.template_updated": "更新答题卡模板",
  "paper.template_cloned": "复制答题卡模板",
  "paper.template_locked": "锁定答题卡模板",
  "paper.question_created": "创建题目",
  "paper.question_updated": "更新题目",
  "paper.question_deleted": "删除题目",
  "paper.rubric_created": "创建评分细则",
  "submission.created": "登记答卷",
  "submission.status_changed": "答卷状态变更",
  "submission.page_added": "补充答卷页",
  "submission.page_replaced": "替换答卷页",
  "submission.quality_checked": "答卷质量检查",
  "submission.pages_processing_started": "开始处理答卷页",
  "ocr.task_created": "创建识别任务",
  "ocr.task_started": "开始文字识别",
  "ocr.task_completed": "文字识别完成",
  "ocr.task_failed": "文字识别失败",
  "grading.scoring_run_started": "开始自动评分",
  "grading.scoring_run_cancelled": "取消自动评分",
  "grading.scoring_run_retry_failed": "重试评分失败项",
  "grading.segment_score_reprocessed": "重新评分",
  "grading.scoring_rule_created": "创建评分规则",
  "grading.scoring_rule_updated": "更新评分规则",
  "grading.scoring_rule_published": "发布评分规则",
  "grading.rule_grade_created": "生成规则评分",
  "grading.answer_recorded": "登记作答内容",
  "grading.omr_completed": "填涂识别完成",
  "grading.omr_failed": "填涂识别失败",
  "grading.omr_calibration_created": "创建填涂识别校准",
  "grading.omr_calibration_case_labeled": "标注填涂校准样例",
  "grading.omr_calibration_approved": "通过填涂识别校准",
  "grading.omr_calibration_revoked": "撤销填涂识别校准",
  "grading.omr_calibration_discarded": "废弃填涂识别校准",
  "review.draft_saved": "保存阅卷草稿",
  "review.human_grade_submitted": "提交人工评分",
  "review.task_created": "创建阅卷任务",
  "review.task_assigned": "分配阅卷任务",
  "review.tasks_batch_assigned": "批量分配阅卷任务",
  "review.task_claimed": "领取阅卷任务",
  "review.task_returned": "退回阅卷任务",
  "review.task_released": "释放阅卷任务",
  "review.double_mark_policy_set": "设置双评策略",
  "review.double_mark_session_created": "创建双评",
  "review.double_mark_auto_finalized": "双评自动定分",
  "arbitration.task_created": "创建仲裁任务",
  "arbitration.task_assigned": "分配仲裁任务",
  "arbitration.submitted": "提交仲裁结果",
  "appeal.created": "提交申诉",
  "appeal.assigned": "分配申诉处理人",
  "appeal.teacher_recommendation_submitted": "提交申诉处理建议",
  "appeal.reviewed": "复核申诉",
  "appeal.score_adjusted": "申诉调分",
  "appeal.closed": "关闭申诉",
  "final_grade.created": "生成最终成绩",
  "score.confirmed": "确认成绩",
  "score.finalized": "成绩定稿",
  "score.published": "发布考试成绩",
  "score_release.published": "发布正式成绩",
  "score.exported": "导出内部原始成绩",
  "score.report_exported": "导出内部核验表",
  "score_release.formal_exported": "导出正式成绩表",
  "report.generated": "生成学情报告",
  "report.exported": "导出学情报告",
  "file.uploaded": "上传文件",
  "file.downloaded": "下载答卷文件",
  "file.deleted": "删除文件",
  "audit.exported": "导出操作记录"
};

export function actionLabel(action: string) {
  return actionLabels[action] ?? "其他操作";
}

// 2026-11 前系统写入的英文说明；新记录已改为中文，旧记录在显示时对照翻译，数据库原值不改。
export const legacySystemReasons: Record<string, string> = {
  "finalize exam grades": "汇总考试成绩",
  "confirm exam grades": "确认考试成绩",
  "publish exam grades": "发布考试成绩（旧流程）",
  "export exam grades": "导出内部核对成绩（非正式版本）",
  "export internal non-official exam grades": "导出内部核对成绩（非正式版本）",
  "export internal non-official exam score report": "导出整场考试内部成绩报表（非正式版本）",
  "export published score snapshots": "导出已发布的正式成绩",
  "view released anonymous high-score paper page": "学生查看已发布的匿名高分卷页面"
};

/** 说明列：旧记录把动作代码原样写在说明里，与“操作”列重复时不再显示。 */
export function auditReasonText(action: string, reason?: string) {
  const value = reason?.trim();
  if (!value || value === action) return "-";
  return legacySystemReasons[value] ?? maskText(value);
}

export function exportWatermarkText(watermark: string) {
  if (!watermark) {
    return "每次导出都会自动记录在操作日志中";
  }
  let displayWatermark = watermark;
  if (watermark.length > 12) {
    displayWatermark = `${watermark.slice(0, 12)}…`;
  }
  return `最近一次导出已记录（水印编号 ${displayWatermark}）`;
}

// 详情只接收展示数据和关闭回调；选中状态、触发元素及焦点恢复仍由父页维护。
export function AuditLogDetailDrawer({ selectedLog, open, onClose, actorName }: Readonly<AuditLogDetailDrawerProps>) {
  return (
    <Drawer title="记录详情" width={640} open={open} onClose={onClose}>
      {selectedLog ? (
        <div className="audit-detail-stack">
          <Descriptions size="small" column={1}>
            <Descriptions.Item label="操作人">{selectedLog.actor_id ? <span title={selectedLog.actor_id}>{actorName(selectedLog.actor_id)}</span> : actorName(selectedLog.actor_id)}</Descriptions.Item>
            <Descriptions.Item label="操作内容"><span title={selectedLog.action}>{actionLabel(selectedLog.action)}</span></Descriptions.Item>
            <Descriptions.Item label="操作对象"><span title={selectedLog.target_id ? `${selectedLog.target_type}:${selectedLog.target_id}` : selectedLog.target_type}>{describeTarget(selectedLog)}</span></Descriptions.Item>
            <Descriptions.Item label="说明">{auditReasonText(selectedLog.action, selectedLog.reason)}</Descriptions.Item>
            <Descriptions.Item label="IP 地址">{selectedLog.ip_address || "-"}</Descriptions.Item>
            <Descriptions.Item label="浏览器">{maskText(selectedLog.user_agent)}</Descriptions.Item>
            <Descriptions.Item label="时间">{formatTime(selectedLog.created_at)}</Descriptions.Item>
            <Descriptions.Item label="请求编号">{selectedLog.request_id || "-"}</Descriptions.Item>
          </Descriptions>
          <details className="audit-json-block">
            <summary><strong>变更前数据</strong></summary>
            <pre>{maskedJSON(selectedLog.before_value)}</pre>
          </details>
          <details className="audit-json-block">
            <summary><strong>变更后数据</strong></summary>
            <pre>{maskedJSON(selectedLog.after_value)}</pre>
          </details>
          <Alert
            type="info"
            showIcon
            icon={<FileWarning size={18} />}
            message="只读记录"
            description="操作记录不可修改、不可删除；密码等敏感内容显示时已自动隐藏。"
          />
        </div>
      ) : (
        <EmptyState title="未选择记录" description="请选择一条操作记录查看详情。" />
      )}
    </Drawer>
  );
}
