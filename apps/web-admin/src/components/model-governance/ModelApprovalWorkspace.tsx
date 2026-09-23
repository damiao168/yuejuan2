import {
  Alert,
  App,
  Button,
  Drawer,
  Empty,
  Form,
  Input,
  InputNumber,
  Modal,
  Select,
  Tag,
  type TableColumnsType
} from "antd";
import { Ban, Clock3, Plus, ShieldCheck } from "lucide-react";
import { useMemo, useState } from "react";
import { getUserErrorMessage } from "../../api/client";
import {
  createModelApproval,
  revokeModelApproval,
  type EvaluationRun,
  type ModelApproval
} from "../../api/modelGovernance";
import { ResponsiveTable } from "../ResponsiveTable";

type ApprovalState = "active" | "expired" | "revoked";

interface ApprovalFormValues {
  evaluation_run_id: string;
  model_config_id: string;
  manual_review_percent: number;
  decision_reference: string;
  expires_at: string;
  reason: string;
}

const stateLabels: Record<ApprovalState, string> = {
  active: "有效",
  expired: "已过期",
  revoked: "已撤销"
};

function approvalState(approval: ModelApproval): ApprovalState {
  if (approval.revoked_at) return "revoked";
  return new Date(approval.expires_at).getTime() > Date.now() ? "active" : "expired";
}

function stateColor(state: ApprovalState) {
  if (state === "active") return "success";
  if (state === "expired") return "warning";
  return "default";
}

function formatTime(value?: string) {
  if (!value) return "-";
  const parsed = new Date(value);
  return Number.isNaN(parsed.getTime()) ? value : parsed.toLocaleString("zh-CN", { hour12: false });
}

function localDateTimeValue(value: Date) {
  const shifted = new Date(value.getTime() - value.getTimezoneOffset() * 60_000);
  return shifted.toISOString().slice(0, 16);
}

function errorMessage(error: unknown) {
  return getUserErrorMessage(error, "模型批准操作失败");
}

export function ModelApprovalWorkspace({
  tenantID,
  approvals,
  evaluationRuns,
  canManage,
  onRefresh
}: {
  tenantID: string;
  approvals: ModelApproval[];
  evaluationRuns: EvaluationRun[];
  canManage: boolean;
  onRefresh: () => Promise<void>;
}) {
  const { message } = App.useApp();
  const [drawerOpen, setDrawerOpen] = useState(false);
  const [revokeTarget, setRevokeTarget] = useState<ModelApproval>();
  const [saving, setSaving] = useState(false);
  const [form] = Form.useForm<ApprovalFormValues>();
  const [revokeForm] = Form.useForm<{ reason: string }>();
  const selectedRunID = Form.useWatch("evaluation_run_id", form);

  const eligibleRuns = useMemo(
    () => evaluationRuns.filter(
      (run) => run.status === "completed" && run.evidence_class === "authorized_frozen_set"
    ),
    [evaluationRuns]
  );
  const selectedRun = eligibleRuns.find((run) => run.id === selectedRunID);

  const openCreate = () => {
    form.resetFields();
    form.setFieldsValue({
      manual_review_percent: 100,
      expires_at: localDateTimeValue(new Date(Date.now() + 90 * 24 * 60 * 60 * 1000))
    });
    setDrawerOpen(true);
  };

  const submitCreate = async () => {
    const values = await form.validateFields();
    setSaving(true);
    try {
      await createModelApproval({
        tenant_id: tenantID,
        evaluation_run_id: values.evaluation_run_id,
        model_config_id: values.model_config_id,
        manual_review_rate: values.manual_review_percent / 100,
        decision_reference: values.decision_reference,
        expires_at: new Date(values.expires_at).toISOString(),
        reason: values.reason
      });
      setDrawerOpen(false);
      message.success("题型级模型批准已生效");
      await onRefresh();
    } catch (error) {
      message.error(errorMessage(error));
    } finally {
      setSaving(false);
    }
  };

  const submitRevoke = async () => {
    if (!revokeTarget) return;
    const { reason } = await revokeForm.validateFields();
    setSaving(true);
    try {
      await revokeModelApproval(tenantID, revokeTarget.id, reason, revokeTarget.revision);
      setRevokeTarget(undefined);
      revokeForm.resetFields();
      message.success("模型批准已撤销，新任务必须停止使用该范围");
      await onRefresh();
    } catch (error) {
      message.error(errorMessage(error));
    } finally {
      setSaving(false);
    }
  };

  const columns: TableColumnsType<ModelApproval> = [
    {
      title: "模型与适用范围",
      key: "scope",
      render: (_value, approval) => (
        <div className="model-governance-stack">
          <span>{approval.model_name || "历史模型配置（已停用）"}</span>
          <small>{approval.subject} · {approval.grade} · {approval.question_type} · {approval.modality}</small>
        </div>
      )
    },
    {
      title: "固定版本",
      key: "versions",
      render: (_value, approval) => (
        <div className="model-governance-stack">
          <span>{approval.model_version}</span>
          <small>{approval.prompt_version} · {approval.rubric_version}</small>
        </div>
      )
    },
    {
      title: "质量证据",
      key: "evidence",
      render: (_value, approval) => (
        <div className="model-governance-stack">
          <span>{approval.dataset_reference}</span>
          <small>{approval.authorization_reference}</small>
        </div>
      )
    },
    {
      title: "人工复核率",
      dataIndex: "manual_review_rate",
      width: 112,
      render: (value: number) => `${(value * 100).toFixed(0)}%`
    },
    {
      title: "决策 / 到期",
      key: "decision",
      render: (_value, approval) => (
        <div className="model-governance-stack">
          <span>{approval.decision_reference}</span>
          <small>{formatTime(approval.expires_at)}</small>
        </div>
      )
    },
    {
      title: "状态",
      key: "status",
      width: 92,
      render: (_value, approval) => {
        const state = approvalState(approval);
        return <Tag color={stateColor(state)}>{stateLabels[state]}</Tag>;
      }
    },
    {
      title: "操作",
      key: "action",
      width: 92,
      render: (_value, approval) => canManage && approvalState(approval) !== "revoked" ? (
        <Button
          danger
          type="link"
          size="small"
          icon={<Ban size={14} />}
          onClick={() => {
            revokeForm.resetFields();
            setRevokeTarget(approval);
          }}
        >
          撤销
        </Button>
      ) : <span className="muted">-</span>
    }
  ];

  return (
    <div className="model-approval-workspace">
      <div className="model-approval-heading">
        <div>
          <span><ShieldCheck size={16} /> 题型级批准</span>
          <p>只有完整授权冻结集证据可以批准；模型、Prompt、Rubric 或适用范围任一变化都会失配。</p>
        </div>
        {canManage ? (
          <Button type="primary" icon={<Plus size={15} />} disabled={eligibleRuns.length === 0} onClick={openCreate}>
            新建批准
          </Button>
        ) : null}
      </div>
      <Alert
        className="model-approval-boundary"
        type="warning"
        showIcon
        icon={<Clock3 size={17} />}
        message="批准有明确有效期，过期或撤销后默认关闭"
        description="批准只允许进入受控教师建议，不允许模型直接写入最终成绩；租户外发开关、预算和健康门禁仍须同时满足。"
      />
      <div className="model-governance-table">
        <ResponsiveTable
          className="dense-data-table"
          rowKey="id"
          columns={columns}
          dataSource={approvals}
          pagination={{ pageSize: 10, hideOnSinglePage: true }}
          locale={{ emptyText: <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="暂无题型级模型批准" /> }}
        />
      </div>

      <Drawer
        title="新建题型级模型批准"
        width={600}
        open={drawerOpen}
        onClose={() => setDrawerOpen(false)}
        extra={<Button type="primary" loading={saving} onClick={() => void submitCreate()}>确认批准</Button>}
      >
        <Alert
          type="info"
          showIcon
          message="批准范围由冻结证据自动带入"
          description="不能在这里修改学科、年级、题型或版本；若范围不正确，应重新生成授权冻结集评测。"
        />
        <Form form={form} layout="vertical" className="model-evaluation-form">
          <Form.Item name="evaluation_run_id" label="授权冻结集评测" rules={[{ required: true }]}>
            <Select
              onChange={() => form.setFieldValue("model_config_id", undefined)}
              options={eligibleRuns.map((run) => ({
                value: run.id,
                label: `${run.display_name} · ${run.subject}/${run.grade}/${run.question_type}`
              }))}
            />
          </Form.Item>
          <Form.Item name="model_config_id" label="评测模型" rules={[{ required: true }]}>
            <Select
              disabled={!selectedRun}
              options={(selectedRun?.candidates ?? []).filter((candidate) => candidate.model_config_id).map((candidate) => ({
                value: candidate.model_config_id!,
                label: `${candidate.model_name} · ${candidate.model_version} · 教师接受率 ${(candidate.metrics.teacher_acceptance_rate * 100).toFixed(1)}%`
              }))}
            />
          </Form.Item>
          <div className="model-governance-form-row two">
            <Form.Item
              name="manual_review_percent"
              label="上线人工复核率"
              rules={[{ required: true }]}
            >
              <InputNumber min={0} max={100} precision={0} addonAfter="%" />
            </Form.Item>
            <Form.Item
              name="expires_at"
              label="批准到期时间"
              rules={[
                { required: true },
                {
                  validator: async (_rule, value?: string) => {
                    if (!value || new Date(value).getTime() <= Date.now()) {
                      throw new Error("到期时间必须晚于当前时间");
                    }
                  }
                }
              ]}
            >
              <Input type="datetime-local" />
            </Form.Item>
          </div>
          <Form.Item
            name="decision_reference"
            label="决策引用"
            rules={[{ required: true }, { pattern: /^[a-z0-9][a-z0-9._-]{0,127}$/ }]}
          >
            <Input placeholder="teaching-review-2026-001" />
          </Form.Item>
          <Form.Item name="reason" label="批准原因" rules={[{ required: true, min: 4, max: 256 }]}>
            <Input.TextArea rows={4} placeholder="说明教研评审结论、适用边界和人工抽样安排" />
          </Form.Item>
        </Form>
      </Drawer>

      <Modal
        title="撤销题型级模型批准"
        open={Boolean(revokeTarget)}
        okText="确认撤销"
        okButtonProps={{ danger: true }}
        confirmLoading={saving}
        onOk={() => void submitRevoke()}
        onCancel={() => setRevokeTarget(undefined)}
      >
        <Alert
          type="error"
          showIcon
          message="撤销后，新任务不能继续使用该模型范围"
          description="历史建议和批准证据保留用于回放与审计，不会被删除。"
        />
        <Form form={revokeForm} layout="vertical" className="model-evaluation-transition-form">
          <Form.Item name="reason" label="撤销原因" rules={[{ required: true, min: 4, max: 256 }]}>
            <Input.TextArea rows={3} placeholder="填写质量变化、授权撤回或版本替换等原因" />
          </Form.Item>
        </Form>
      </Modal>
    </div>
  );
}
