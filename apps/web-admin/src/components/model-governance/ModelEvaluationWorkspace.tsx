import { Alert, Button, Drawer, Empty, Form, Input, InputNumber, Modal, Select, Space, Tag, Tooltip } from "antd";
import { Ban, BarChart3, CheckCircle2, Plus, Search } from "lucide-react";
import { EvaluationRun } from "../../api/modelGovernance";
import { modelConfigOptionLabel } from "../../features/platform-model-config/lib/modelConfig";
import { ResponsiveTable } from "../ResponsiveTable";
import { microsToCost } from "./costUnits";
import { evaluationMaterialMatchesRun, parseEvaluationRunMaterial } from "./evaluationReportImport";
import { downloadGovernanceMaterial, evaluationReportExample } from "./governanceMaterials";
import { errorMessage, evidenceLabels, renderRunEvidence, statusColor, statusLabels, useModelEvaluationWorkspace } from "./useModelEvaluationWorkspace";
export function ModelEvaluationWorkspace(props: Parameters<typeof useModelEvaluationWorkspace>[0]) {
  const {
    runs, tenantID, loading, canManage,
    keyword, setKeyword, status,
    setStatus, evidenceClass, setEvidenceClass, selectedRunID,
    createDrawerOpen, setCreateDrawerOpen, candidateDrawerOpen, setCandidateDrawerOpen,
    transitionKind, setTransitionKind, saving, interactionVersion,
    mounted, createScope, candidateScope, createForm,
    importNotice, setImportNotice, setImportedMaterial, candidateImportNotice,
    setCandidateImportNotice, candidateForm, transitionForm, filteredRuns,
    selectedRun, currentContext, selectRun, canMutateRun,
    availableConfigs, importedCandidates, canCompleteSelected, columns,
    candidateColumns, openCreateDrawer, openCandidateDrawer, submitCreate,
    submitCandidate, submitTransition, openTransition, candidateDrawerTitle,
  } = useModelEvaluationWorkspace(props);
  const renderSelectedRun = (run: EvaluationRun) => {
    // 这里只复用已有展示条件；冻结后不再显示录入和再次冻结入口。
    const canEditRun = canManage && run.status === "draft";
    return (
      <section className="model-evaluation-detail" aria-label="评测候选对比">
        <header>
          <div>
            <span className="model-evaluation-detail-kicker"><BarChart3 size={15} /> 候选并排对比</span>
            <h2>{run.display_name}</h2>
            <p>
              {run.subject} · {run.grade} · {run.question_type}
              <span>数据集 {run.dataset_reference}</span>
            </p>
          </div>
          <Space wrap>
            <Tag color={statusColor(run.status)}>{statusLabels[run.status]}</Tag>
            {canEditRun ? (
              <Button icon={<Plus size={15} />} disabled={availableConfigs.length === 0} onClick={openCandidateDrawer}>
                添加候选
              </Button>
            ) : null}
            {canEditRun ? (
              <Tooltip title={canCompleteSelected ? undefined : "至少需要两个候选；新评测请选择两个学校模型"}>
                <span>
                  <Button type="primary" icon={<CheckCircle2 size={15} />} disabled={!canCompleteSelected} onClick={() => openTransition("complete")}>
                    冻结完成
                  </Button>
                </span>
              </Tooltip>
            ) : null}
            {canManage && run.status !== "invalidated" ? (
              <Button danger icon={<Ban size={15} />} onClick={() => openTransition("invalidate")}>
                标记失效
              </Button>
            ) : null}
          </Space>
        </header>
        <div className="model-evaluation-evidence">{renderRunEvidence(run)}</div>
        <div className="model-evaluation-candidate-table">
          <ResponsiveTable
            rowKey="id"
            columns={candidateColumns}
            dataSource={run.candidates}
            pagination={false}
            mobilePrimaryCount={4}
            locale={{ emptyText: <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="该批次还没有候选结果" /> }}
          />
        </div>
        <footer>
          <span><i className="model-evaluation-legend-best" /> 同批次内的相对最优值</span>
          <small>不同数据集、题型或 Rubric 版本之间不可直接横向比较。</small>
        </footer>
      </section>
    );
  };

  return (
    <div className="model-evaluation-workspace">
      <div className="model-evaluation-filterbar">
        <Input
          allowClear
          prefix={<Search size={15} />}
          placeholder="搜索批次、数据集或适用范围"
          value={keyword}
          onChange={(event) => setKeyword(event.target.value)}
        />
        <Space wrap>
          <Select
            aria-label="评测状态"
            value={status}
            onChange={setStatus}
            options={[
              { value: "all", label: "全部状态" },
              { value: "draft", label: statusLabels.draft },
              { value: "completed", label: statusLabels.completed },
              { value: "invalidated", label: statusLabels.invalidated }
            ]}
          />
          <Select
            aria-label="证据类别"
            value={evidenceClass}
            onChange={setEvidenceClass}
            options={[
              { value: "all", label: "全部证据" },
              { value: "protocol_fixture", label: evidenceLabels.protocol_fixture },
              { value: "authorized_frozen_set", label: evidenceLabels.authorized_frozen_set }
            ]}
          />
          {canManage ? (
            <Button type="primary" icon={<Plus size={15} />} onClick={openCreateDrawer}>
              登记已有评测报告
            </Button>
          ) : null}
          <Button onClick={() => downloadGovernanceMaterial("候选模型评测报告示例.json", evaluationReportExample)}>下载报告材料模板</Button>
        </Space>
      </div>
      <div className="model-evaluation-count">
        <span>显示 {filteredRuns.length} / {runs.length} 个批次</span>
        <small>这里只记录离线对比证据，不改变当前评分路由。</small>
      </div>
      <div className="model-governance-table">
        <ResponsiveTable
          className="dense-data-table"
          rowKey="id"
          columns={columns}
          dataSource={filteredRuns}
          loading={loading}
          pagination={{ pageSize: 10, hideOnSinglePage: true }}
          locale={{ emptyText: <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="暂无符合条件的评测批次" /> }}
          onRow={(run) => ({ onClick: () => selectRun(run.id) })}
          rowClassName={(run) => run.id === selectedRunID ? "model-evaluation-selected-row" : ""}
        />
      </div>
      {selectedRun ? renderSelectedRun(selectedRun) : null}

      <Drawer
        title="登记已有离线评测报告"
        width={620}
        open={createDrawerOpen}
        onClose={() => { interactionVersion.current += 1; setCreateDrawerOpen(false); }}
        extra={<Button type="primary" loading={saving} onClick={() => void submitCreate()}>创建批次</Button>}
      >
        <Alert
          type="info"
          showIcon
          message="登记已有报告，不运行模型"
          description="由评测负责人先完成授权数据的离线评分和人工核验，再登记数据集引用、版本与候选汇总。登记后仍须冻结和人工批准；不会调用外部模型或改变评分路由。"
        />
        <Form name="model-evaluation-create" form={createForm} layout="vertical" className="model-evaluation-form">
          <label>导入已有报告 JSON（仅预填）<input type="file" accept=".json,application/json" disabled={saving} onChange={async event => {
            const file = event.target.files?.[0]; event.target.value = "";
            if (!file) return;
            const owner = createScope.current; const version = interactionVersion.current;
            try {
              if (file.size > 2 * 1024 * 1024) throw new Error("报告请控制在 2 MB 以内");
              const material = parseEvaluationRunMaterial(await file.text());
              if (!mounted.current || owner !== tenantID || createScope.current !== owner || interactionVersion.current !== version) return;
              createForm.setFieldsValue(material.run);
              setImportedMaterial({ tenantID, material });
              setImportNotice(`已预填批次元数据和 ${material.candidateCount} 个候选指标；请核对哈希、授权引用及统计范围。创建批次后可选择报告中的候选，核对后分别保存。`);
            } catch (cause) { if (mounted.current && createScope.current === owner && interactionVersion.current === version) setImportNotice(errorMessage(cause)); }
          }} /></label>
          {importNotice ? <p role="status">{importNotice}</p> : null}
          <div className="model-governance-form-row two">
            <Form.Item
              name="run_key"
              label="批次标识"
              rules={[{ required: true }, { pattern: /^[a-z0-9][a-z0-9._-]{0,127}$/, message: "仅支持小写字母、数字、点、下划线和连字符" }]}
            >
              <Input placeholder="math-grade9-fixture-v1" />
            </Form.Item>
            <Form.Item name="display_name" label="批次名称" rules={[{ required: true, max: 128 }]}>
              <Input placeholder="九年级数学协议样例对比" />
            </Form.Item>
          </div>
          <div className="model-governance-form-row two">
            <Form.Item
              name="dataset_reference"
              label="数据集引用"
              rules={[{ required: true }, { pattern: /^[a-z0-9][a-z0-9._-]{0,127}$/ }]}
            >
              <Input placeholder="math-grade9-fixture-v1" />
            </Form.Item>
            <Form.Item
              name="dataset_sha256"
              label="数据集 SHA-256"
              rules={[{ required: true }, { pattern: /^[a-f0-9]{64}$/, message: "请输入 64 位小写 SHA-256" }]}
            >
              <Input placeholder="64 位小写哈希" />
            </Form.Item>
          </div>
          <Form.Item name="evidence_class" label="证据类别" rules={[{ required: true }]}>
            <Select
              options={[
                { value: "protocol_fixture", label: "协议样例（不代表模型效果）" },
                { value: "authorized_frozen_set", label: "授权冻结集（含教师真值）" }
              ]}
            />
          </Form.Item>
          <Form.Item noStyle shouldUpdate={(before, after) => before.evidence_class !== after.evidence_class}>
            {({ getFieldValue }) => getFieldValue("evidence_class") === "authorized_frozen_set" ? (
              <Form.Item
                name="authorization_reference"
                label="授权引用"
                rules={[{ required: true }, { pattern: /^[a-z0-9][a-z0-9._-]{0,127}$/ }]}
              >
                <Input placeholder="frozen-set-approval-2026-01" />
              </Form.Item>
            ) : null}
          </Form.Item>
          <div className="model-governance-form-row">
            <Form.Item name="subject" label="学科" rules={[{ required: true, max: 128 }]}><Input placeholder="数学" /></Form.Item>
            <Form.Item name="grade" label="年级" rules={[{ required: true, max: 128 }]}><Input placeholder="九年级" /></Form.Item>
            <Form.Item
              name="question_type"
              label="题型标识"
              rules={[{ required: true }, { pattern: /^[a-z0-9][a-z0-9._-]{0,127}$/ }]}
            >
              <Input placeholder="short_answer" />
            </Form.Item>
          </div>
          <div className="model-governance-form-row">
            <Form.Item name="modality" label="输入模态" rules={[{ required: true }]}>
              <Select options={[{ value: "text", label: "文本" }, { value: "image", label: "图片" }]} />
            </Form.Item>
            <Form.Item name="sample_count" label="样本数" rules={[{ required: true }]}>
              <InputNumber min={1} max={100000} precision={0} />
            </Form.Item>
            <Form.Item name="repeat_count" label="重复评分次数" rules={[{ required: true }]}>
              <InputNumber min={1} max={20} precision={0} />
            </Form.Item>
          </div>
          <Form.Item name="reason" label="创建原因" rules={[{ required: true, min: 4, max: 256 }]}>
            <Input.TextArea rows={3} placeholder="说明评测目的、证据来源和复核安排" />
          </Form.Item>
        </Form>
      </Drawer>

      <Drawer
        title={candidateDrawerTitle}
        width={620}
        open={candidateDrawerOpen}
        onClose={() => { interactionVersion.current += 1; setCandidateDrawerOpen(false); }}
        extra={<Button type="primary" loading={saving} onClick={() => void submitCandidate()}>保存候选</Button>}
      >
        {selectedRun ? (
          <>
            <Alert
              type={selectedRun.evidence_class === "protocol_fixture" ? "info" : "success"}
              showIcon
              message={selectedRun.evidence_class === "protocol_fixture" ? "协议样例不录入教师接受数据" : "教师复核数固定为全部样本"}
              description={`批次固定 ${selectedRun.sample_count} 个样本、重复 ${selectedRun.repeat_count} 次；候选版本写入后不可修改。`}
            />
            {/* 抽屉关闭后仍保留表单；独立名称避免与批准表单的同名字段争用 DOM id 和标签关联。 */}
            <Form name="model-evaluation-candidate" form={candidateForm} layout="vertical" className="model-evaluation-form">
              <label>读取候选报告 JSON（仅预填）<input type="file" accept=".json,application/json" disabled={saving} onChange={async event => {
                const file = event.target.files?.[0]; event.target.value = "";
                if (!file) return;
                const scope = candidateScope.current; const version = interactionVersion.current;
                try {
                  if (file.size > 2 * 1024 * 1024) throw new Error("报告请控制在 2 MB 以内");
                  const material = parseEvaluationRunMaterial(await file.text());
                  if (!scope || !mounted.current || scope !== candidateScope.current || interactionVersion.current !== version || currentContext.current.tenantID !== scope.tenantID) return;
                  const currentRun = currentContext.current.run;
                  if (!currentRun || !evaluationMaterialMatchesRun(material, currentRun)) throw new Error("报告的批次、数据集、授权或样本范围与当前批次不一致");
                  setImportedMaterial({ tenantID: scope.tenantID, material });
                  setCandidateImportNotice("报告已读取。请选择当前学校已验证的模型候选，核对指标后保存。");
                } catch (cause) { if (mounted.current && scope === candidateScope.current && interactionVersion.current === version) setCandidateImportNotice(errorMessage(cause)); }
              }} /></label>
              {importedCandidates.length ? <Form.Item label="从报告预填候选"><Select placeholder="选择待核对的候选指标" value={undefined} disabled={saving} options={importedCandidates.map(candidate => ({ value: candidate.model_config_id, label: `${modelConfigOptionLabel(availableConfigs.find(config => config.id === candidate.model_config_id)!)} · ${candidate.prompt_version}` }))} onChange={id => {
                const candidate = importedCandidates.find(item => item.model_config_id === id);
                if (!candidate || !selectedRun || !canMutateRun(selectedRun.id, "candidate")) return;
                candidateForm.setFieldsValue({ ...candidate, total_cost_micros: microsToCost(candidate.total_cost_micros) });
                setCandidateImportNotice("已预填候选版本与指标，请核对后保存；尚未登记或冻结。");
              }} /></Form.Item> : null}
              {candidateImportNotice ? <p role="status">{candidateImportNotice}</p> : null}
              <Form.Item name="model_config_id" label="评测模型" rules={[{ required: true }]}>
                <Select
                  options={availableConfigs.map((config) => ({
                    value: config.id,
                    label: modelConfigOptionLabel(config)
                  }))}
                />
              </Form.Item>
              <div className="model-governance-form-row two">
                <Form.Item name="prompt_version" label="提示词版本" rules={[{ required: true, max: 128 }]}><Input placeholder="prompt-v1" /></Form.Item>
                <Form.Item name="rubric_version" label="Rubric 版本" rules={[{ required: true, max: 128 }]}><Input placeholder="rubric-v1" /></Form.Item>
              </div>
              <div className="model-governance-form-row">
                <Form.Item name="evaluated_samples" label="已评样本"><InputNumber disabled /></Form.Item>
                <Form.Item name="teacher_reviewed_samples" label="教师复核数"><InputNumber disabled /></Form.Item>
                <Form.Item
                  name="teacher_accepted_samples"
                  label="教师直接接受数"
                  rules={[{ required: true }]}
                >
                  <InputNumber
                    disabled={selectedRun.evidence_class === "protocol_fixture"}
                    min={0}
                    max={selectedRun.sample_count}
                    precision={0}
                  />
                </Form.Item>
              </div>
              <div className="model-governance-form-row">
                <Form.Item name="serious_error_samples" label="严重错误样本" rules={[{ required: true }]}>
                  <InputNumber min={0} max={selectedRun.sample_count} precision={0} />
                </Form.Item>
                <Form.Item name="evidence_valid_samples" label="证据有效样本" rules={[{ required: true }]}>
                  <InputNumber min={0} max={selectedRun.sample_count} precision={0} />
                </Form.Item>
                <Form.Item name="p95_latency_ms" label="P95 时延（ms）" rules={[{ required: true }]}>
                  <InputNumber min={0} precision={0} />
                </Form.Item>
              </div>
              <div className="model-governance-form-row">
                <Form.Item name="repeat_comparisons" label="重复对比数" rules={[{ required: true }]}>
                  <InputNumber min={0} max={selectedRun.sample_count * Math.max(0, selectedRun.repeat_count - 1)} precision={0} />
                </Form.Item>
                <Form.Item name="stable_repeat_samples" label="稳定重复数" rules={[{ required: true }]}>
                  <InputNumber min={0} max={selectedRun.sample_count * Math.max(0, selectedRun.repeat_count - 1)} precision={0} />
                </Form.Item>
                <Form.Item name="total_cost_micros" label="总预估成本（美元）" extra="按微美元口径登记已有报告成本，不是供应商账单。" rules={[{ required: true }]}>
                  <InputNumber stringMode min="0" precision={6} />
                </Form.Item>
              </div>
              <Form.Item name="reason" label="录入原因" rules={[{ required: true, min: 4, max: 256 }]}>
                <Input.TextArea rows={3} placeholder="说明离线运行来源和数据核验情况" />
              </Form.Item>
            </Form>
          </>
        ) : null}
      </Drawer>

      <Modal
        title={transitionKind === "complete" ? "冻结完成评测" : "标记评测证据失效"}
        open={Boolean(transitionKind)}
        okText={transitionKind === "complete" ? "确认冻结" : "确认失效"}
        okButtonProps={{ danger: transitionKind === "invalidate" }}
        confirmLoading={saving}
        onOk={() => void submitTransition()}
        onCancel={() => { interactionVersion.current += 1; setTransitionKind(undefined); }}
      >
        <Alert
          type={transitionKind === "complete" ? "warning" : "error"}
          showIcon
          message={transitionKind === "complete" ? "冻结后不能再添加候选" : "失效后只能保留为历史审计证据"}
          description={transitionKind === "complete"
            ? "本操作仍不会批准模型或改变评分路由。"
            : "若数据授权撤回、版本不一致或证据被替代，应立即标记失效。"}
        />
        <Form name="model-evaluation-transition" form={transitionForm} layout="vertical" className="model-evaluation-transition-form">
          <Form.Item name="reason" label="操作原因" rules={[{ required: true, min: 4, max: 256 }]}>
            <Input.TextArea rows={3} placeholder="填写可审计的操作原因" />
          </Form.Item>
        </Form>
      </Modal>
    </div>
  );
}
