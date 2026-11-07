import { Alert, Button, Card, Divider, Drawer, Empty, Form, Input, InputNumber, Select, Space, Table } from "antd";
import { FlaskConical, RefreshCw, ShieldAlert } from "lucide-react";
import { DisagreementTaskSelector } from "./DisagreementTaskSelector";
import { eligibilityArchetypes as archetypeOptions, eligibilityRisks, eligibilityScopeLabel, eligibilityStages, isCompleteEligibilitySelection, eligibilitySubjects as subjectOptions } from "./eligibilityScope";
import { downloadGovernanceMaterial, observationExample } from "./governanceMaterials";
import { ScoringAssuranceProps, taxonomyOptions } from "./scoringAssuranceModel";
import { assuranceErrorSourceOptions as errorSourceOptions } from "./scoringAssurancePresentation";
import { CalibrationDetail, DisagreementQueue, EligibilityPolicyPanel, EvaluationDetail } from "./ScoringAssuranceViews";
import { useScoringAssuranceWorkspace } from "./useScoringAssuranceWorkspace";

import { scoringModeOptions } from "./assuranceOptions";
import { DecisionLookup } from "./DecisionLookup";



export function ScoringAssuranceWorkspace(props: ScoringAssuranceProps) {
  // 学校或入口评分组合改变时重建工作区；旧范围表单和未返回请求不能沿用到新对象。
  return <ScoringAssuranceContent key={JSON.stringify([props.tenantID, props.initialAxis ?? {}])} {...props} />;
}
function ScoringAssuranceContent(props: Parameters<typeof useScoringAssuranceWorkspace>[0]) {
  const {
    tenantID, canReadEligibility, canManageEligibility, canManageEvaluations,
    canReadDisagreements, canManageDisagreements, message, axis,
    policyReadState, policy, policyLoading, policyError,
    policyDrawerOpen, setPolicyDrawerOpen, policySaving, policySavingRef,
    policyForm, changeAxis, evaluationRuns, calibrations,
    assuranceLoading, assuranceError, evaluationDrawerOpen, setEvaluationDrawerOpen,
    observationDrawerOpen, setObservationDrawerOpen, calibrationDrawerOpen, setCalibrationDrawerOpen,
    actioning, openDraft, closeDraft, selectedEvaluationID,
    selectedCalibrationID, sliceMetrics, responseDifficulty, qualitySummary,
    calibrationEvidence, evaluationRead, calibrationRead, setEvaluationRetry,
    setCalibrationRetry, evaluationForm, observationForm, calibrationForm,
    evidenceForm, importText, setImportText, importRows,
    setImportRows, importCompleted, setImportCompleted, importRunID,
    setImportRunID, importOpen, setImportOpen, importError,
    setImportError, sourceMaterial, setSourceMaterial, previewObservations,
    importObservations, disagreements, disagreementLoading, disagreementError,
    selectedDisagreement, setSelectedDisagreement, classificationDrawerOpen, setClassificationDrawerOpen,
    routeDrawerOpen, setRouteDrawerOpen, routeTask, setRouteTask,
    classificationForm, routeForm, loadPolicy, loadAssurance,
    loadDisagreements, selectedEvaluation, selectedCalibration, evaluationReady,
    calibrationReady, openPolicyEditor, savePolicy, createEvaluation,
    addObservation, transitionEvaluation, createCalibration, addCalibrationEvidence,
    transitionCalibration, submitClassification, submitRoute, evaluationColumns,
    calibrationColumns, severeCount, canSeeAny,
  } = useScoringAssuranceWorkspace(props);

  if (!canSeeAny) {
    return <Alert showIcon type="info" message="当前账号没有评分保障查看权限" description="题型准入、离线评测、校准和人机分歧分别由模型治理与阅卷质量权限控制。" />;
  }

  return (
    <div className="model-assurance-workspace">
      <Alert
        showIcon
        type="info"
        icon={<ShieldAlert size={17} />}
        message="评分保障不会授予模型最终分数权"
        description="准入策略、离线评测、置信度校准与人机分歧均是可追溯的安全证据。未满足任何一项时，系统必须转入人工流程。"
      />

      {canReadEligibility ? <EligibilityPolicyPanel axis={axis} onAxisChange={changeAxis} policy={policy} policyLoading={policyLoading}
        selectionLocked={policyDrawerOpen || policySaving} selectionComplete={isCompleteEligibilitySelection(axis)} canEditPolicy={["loaded", "missing"].includes(policyReadState)}
        policyError={policyError} canManageEligibility={canManageEligibility} subjectOptions={subjectOptions} archetypeOptions={archetypeOptions}
        loadPolicy={loadPolicy} openPolicyEditor={openPolicyEditor}><DecisionLookup canRead={canReadEligibility} tenantID={tenantID} /></EligibilityPolicyPanel> : null}

      {canManageEvaluations ? <Card size="small" title={<Space><FlaskConical size={17} />已有评分报告与置信度校准</Space>} extra={<Space><Button icon={<RefreshCw size={15} />} loading={assuranceLoading} onClick={() => void loadAssurance()}>刷新</Button><Button type="primary" onClick={() => openDraft("evaluation", setEvaluationDrawerOpen)}>登记评分验证报告</Button></Space>}>
        <p>评测负责人先完成离线评分及人工真值核验，再登记版本轴和对齐记录。此处不运行模型；冻结证据与配置准入是后续步骤。</p>
        <Space wrap><Button onClick={() => downloadGovernanceMaterial("脱敏对齐评分记录模板.json", observationExample)}>下载对齐记录模板</Button><Button disabled={selectedEvaluation?.status !== "draft" || actioning} onClick={() => {
          if (importText && importRunID !== selectedEvaluation!.id) { message.error("另一评测的导入材料尚未清除，请先回到原评测完成或清除材料。"); return; }
          if (!importText) { setImportRunID(selectedEvaluation!.id); setImportRows([]);setImportCompleted(0);setImportError(""); }
          setImportOpen(true);
        }}>导入对齐评分记录</Button></Space>
        {assuranceError ? <Alert showIcon type="error" message="加载评分保障证据失败" description={assuranceError} action={<Button onClick={() => void loadAssurance()}>重试</Button>} /> : null}
        <Table className="dense-data-table" rowKey="id" size="small" pagination={false} columns={evaluationColumns} dataSource={evaluationRuns} locale={{ emptyText: <Empty description="尚无离线评测" image={Empty.PRESENTED_IMAGE_SIMPLE} /> }} />
        {selectedEvaluation && !evaluationReady ? <Alert showIcon type={evaluationRead.status === "error" && evaluationRead.id === selectedEvaluationID ? "error" : "info"} message={evaluationRead.id === selectedEvaluationID && evaluationRead.error ? evaluationRead.error : "正在读取当前评测证据"} action={evaluationRead.status === "error" && evaluationRead.id === selectedEvaluationID ? <Button onClick={() => setEvaluationRetry(value => value + 1)}>重试评测证据</Button> : undefined} /> : null}
        <EvaluationDetail selectedEvaluation={selectedEvaluation} actioning={actioning} evidenceReady={evaluationReady} qualitySummary={evaluationReady ? qualitySummary : undefined}
          sliceMetrics={evaluationReady ? sliceMetrics : []} responseDifficulty={evaluationReady ? responseDifficulty : []} onOpenObservation={() => openDraft("observation", setObservationDrawerOpen)}
          transitionEvaluation={transitionEvaluation} onCreateCalibration={() => {
            if (!selectedEvaluation) return;
            calibrationForm.setFieldsValue({ evaluation_run_id: selectedEvaluation.id, method: "auto", axis: { model_reference: selectedEvaluation.model_reference, prompt_version: selectedEvaluation.prompt_version, rubric_version: selectedEvaluation.rubric_version, subject: "mathematics", archetype: "short_constructed", slice_key: "all" } });
            openDraft("calibration", setCalibrationDrawerOpen);
          }} />
        <Divider orientation="left">校准工件</Divider>
        <Table className="dense-data-table" rowKey="id" size="small" pagination={false} columns={calibrationColumns} dataSource={calibrations} locale={{ emptyText: "尚无校准工件" }} />
        {selectedCalibration && !calibrationReady ? <Alert showIcon type={calibrationRead.status === "error" && calibrationRead.id === selectedCalibrationID ? "error" : "info"} message={calibrationRead.id === selectedCalibrationID && calibrationRead.error ? calibrationRead.error : "正在读取当前校准证据"} action={calibrationRead.status === "error" && calibrationRead.id === selectedCalibrationID ? <Button onClick={() => setCalibrationRetry(value => value + 1)}>重试校准证据</Button> : undefined} /> : null}
        <CalibrationDetail selectedCalibration={selectedCalibration} calibrationEvidence={calibrationReady ? calibrationEvidence : []} actioning={actioning} evidenceReady={calibrationReady}
          evidenceForm={evidenceForm} transitionCalibration={transitionCalibration} addCalibrationEvidence={addCalibrationEvidence} />
      </Card> : null}

      {canReadDisagreements ? <DisagreementQueue disagreements={disagreements} severeCount={severeCount} disagreementLoading={disagreementLoading}
        disagreementError={disagreementError} canManageDisagreements={canManageDisagreements} taxonomyOptions={taxonomyOptions}
        loadDisagreements={loadDisagreements} onClassify={(item) => {
          setSelectedDisagreement(item); classificationForm.setFieldsValue({ taxonomy: item.taxonomy ?? "ai_scoring_error", notes: item.notes }); openDraft("classification", setClassificationDrawerOpen);
        }} onRoute={(item) => { setSelectedDisagreement(item); setRouteTask(undefined); routeForm.resetFields(); openDraft("route", setRouteDrawerOpen); }} /> : null}

      <Drawer title="维护题型准入策略" width={560} open={policyDrawerOpen} closable={!policySaving} maskClosable={!policySaving} keyboard={!policySaving} onClose={() => { if (!policySavingRef.current) setPolicyDrawerOpen(false); }} extra={<Button type="primary" loading={policySaving} onClick={() => void savePolicy()}>保存新版本</Button>}>
        <p>当前维护范围：{eligibilityScopeLabel(axis)}。需要维护其他范围时，请先关闭并在页面重新选择和查询。</p>
        <Alert type="warning" showIcon message="此处只配置是否允许调用 AI，不授予模型最终分数权。R3 长作答禁止配置为 AI 快速确认或规则自动。" />
        <Form form={policyForm} layout="vertical" style={{ marginTop: 16 }}>
          <Space.Compact block><Form.Item name="subject_code" label="学科" rules={[{ required: true }]} style={{ width: "25%" }}><Select disabled options={subjectOptions} /></Form.Item><Form.Item name="education_stage" label="学段" rules={[{ required: true }]} style={{ width: "25%" }}><Select disabled options={eligibilityStages} /></Form.Item><Form.Item name="risk_tier" label="风险" rules={[{ required: true }]} style={{ width: "20%" }}><Select disabled options={eligibilityRisks} /></Form.Item></Space.Compact>
          <Form.Item name="archetype_code" label="题型" rules={[{ required: true }]}><Select disabled options={archetypeOptions} /></Form.Item>
          <Space.Compact block><Form.Item name="min_ocr_quality" label="最低 OCR 质量" rules={[{ required: true }]} style={{ width: "33%" }}><InputNumber min={0} max={1} step={0.01} style={{ width: "100%" }} /></Form.Item><Form.Item name="min_parser_quality" label="最低解析质量" rules={[{ required: true }]} style={{ width: "33%" }}><InputNumber min={0} max={1} step={0.01} style={{ width: "100%" }} /></Form.Item><Form.Item name="min_eval_n" label="最低评测样本" rules={[{ required: true }]} style={{ width: "34%" }}><InputNumber min={1} precision={0} style={{ width: "100%" }} /></Form.Item></Space.Compact>
          <Form.Item name="max_severe_error_rate" label="最大严重误差率" rules={[{ required: true }]}><InputNumber min={0} max={1} step={0.005} style={{ width: "100%" }} /></Form.Item>
          <Form.Item name="allowed_modes" label="允许评分模式" rules={[{ required: true }]}><Select mode="multiple" options={scoringModeOptions} /></Form.Item>
          <Form.Item name="status" label="状态" rules={[{ required: true }]}><Select options={[{ value: "active", label: "启用" }, { value: "disabled", label: "停用" }]} /></Form.Item>
          <Form.Item name="expected_version" hidden><InputNumber /></Form.Item>
        </Form>
      </Drawer>

      <Drawer title="登记已有评分验证报告" width={620} open={evaluationDrawerOpen} onClose={() => closeDraft(setEvaluationDrawerOpen)} extra={<Button type="primary" loading={actioning} onClick={() => void createEvaluation()}>登记报告</Button>}>
        <Alert type="info" showIcon message="仅登记已授权的脱敏数据集引用和版本轴" description="评测不会在浏览器中调用模型，也不接收学生答案、原图或标准卷解析。" />
        {sourceMaterial ? <p>已带入 {sourceMaterial.observations.length} 条逐卷记录。<Button disabled={actioning} onClick={() => setSourceMaterial(undefined)}>清除原材料</Button></p> : null}
        <Form form={evaluationForm} layout="vertical" style={{ marginTop: 16 }}>
          <Form.Item name="key" label="评测键" rules={[{ required: true, pattern: /^[a-z0-9][a-z0-9._-]{0,127}$/ }]}><Input placeholder="math-r2-v1" /></Form.Item>
          <Form.Item name="display_name" label="名称" rules={[{ required: true }]}><Input /></Form.Item>
          <Form.Item name="model_reference" label="模型版本" rules={[{ required: true }]}><Input /></Form.Item>
          <Space.Compact block><Form.Item name="prompt_version" label="提示词版本" rules={[{ required: true }]} style={{ width: "50%" }}><Input /></Form.Item><Form.Item name="rubric_version" label="评分细则版本" rules={[{ required: true }]} style={{ width: "50%" }}><Input /></Form.Item></Space.Compact>
          <Form.Item name="dataset_reference" label="脱敏数据集引用" rules={[{ required: true, pattern: /^[a-z0-9][a-z0-9._-]{0,127}$/ }]}><Input /></Form.Item>
          <Form.Item name="dataset_sha256" label="数据集 SHA-256" rules={[{ required: true, pattern: /^[a-f0-9]{64}$/ }]}><Input /></Form.Item>
        </Form>
      </Drawer>

      <Drawer title="导入脱敏对齐评分记录" width={680} open={importOpen} closable={!actioning} maskClosable={!actioning} onClose={() => { if (!actioning) setImportOpen(false); }}>
        <p>关闭后材料仍留在本学校工作区，再次打开可继续；离开学校前请复制备份。清除材料不会删除已保存的观察。</p>
        <Button disabled={actioning} onClick={() => { setImportText(""); setImportRows([]); setImportCompleted(0); setImportError(""); setImportOpen(false); }}>清除本次导入材料</Button>
        <Alert type="info" showIcon message="仅导入已完成评分的脱敏记录，不调用模型" description={`目标报告：${evaluationRuns.find((run) => run.id === importRunID)?.display_name ?? importRunID}。逐条保存，没有批量回滚；真实字段和权限由服务端再次校验。`} />
        <label>JSON 观察记录<Input.TextArea rows={10} value={importText} disabled={actioning || importCompleted > 0} onChange={(event) => { setImportText(event.target.value); setImportRows([]); }} /></label>
        <Button disabled={actioning || importCompleted > 0} onClick={previewObservations}>校验并预览</Button>
        {importRows.length ? <><p>共 {importRows.length} 条，已保存 {importCompleted} 条。首条：{importRows[0].response_key}，参考 {importRows[0].reference_score} / 模型 {importRows[0].model_score}。</p><Button type="primary" loading={actioning} disabled={importCompleted === importRows.length} onClick={() => void importObservations()}>{importCompleted ? "继续保存剩余记录" : "确认导入"}</Button></> : null}
        {importError ? <Alert type="error" showIcon message={importError} /> : null}
      </Drawer>

      <Drawer title="录入对齐观察" width={650} open={observationDrawerOpen} onClose={() => closeDraft(setObservationDrawerOpen)} extra={<Button type="primary" loading={actioning} onClick={() => void addObservation()}>保存观察</Button>}>
        <Alert type="info" showIcon message="只记录脱敏键、指纹、切片标签与对齐分数" description="系统不接收答题文本、图片或标准卷解析内容。" />
        <Form form={observationForm} layout="vertical" style={{ marginTop: 16 }} initialValues={{ reference_kind: "gold", ocr_quality: "high", answer_length: "medium", rubric_complexity: "medium", error_source: "none", needs_human_review: false, reference_reviewer_count: 1, reference_adjudicated: false }}>
          <Space.Compact block><Form.Item name="response_key" label="脱敏答卷键" rules={[{ required: true, pattern: /^[a-z0-9][a-z0-9._-]{0,127}$/ }]} style={{ width: "50%" }}><Input /></Form.Item><Form.Item name="response_fingerprint" label="答卷指纹 SHA-256" rules={[{ required: true, pattern: /^[a-f0-9]{64}$/ }]} style={{ width: "50%" }}><Input /></Form.Item></Space.Compact>
          <Space.Compact block><Form.Item name="reference_kind" label="参考来源" rules={[{ required: true }]} style={{ width: "33%" }}><Select options={[{ value: "gold", label: "标准卷" }, { value: "human_adjudicated", label: "双人裁决" }]} onChange={(value) => observationForm.setFieldsValue(value === "human_adjudicated" ? { reference_reviewer_count: 2, reference_adjudicated: true } : { reference_reviewer_count: 1, reference_adjudicated: false })} /></Form.Item><Form.Item name="subject" label="学科切片" rules={[{ required: true, pattern: /^[a-z0-9][a-z0-9._-]{0,127}$/ }]} style={{ width: "33%" }}><Input placeholder="mathematics" /></Form.Item><Form.Item name="archetype" label="题型切片" rules={[{ required: true, pattern: /^[a-z0-9][a-z0-9._-]{0,127}$/ }]} style={{ width: "34%" }}><Input placeholder="structured_steps" /></Form.Item></Space.Compact>
          <Space.Compact block><Form.Item name="ocr_quality" label="OCR 质量" rules={[{ required: true }]} style={{ width: "33%" }}><Select options={[{ value: "high", label: "高" }, { value: "medium", label: "中" }, { value: "low", label: "低" }, { value: "unknown", label: "未知" }]} /></Form.Item><Form.Item name="answer_length" label="答案长度" rules={[{ required: true }]} style={{ width: "33%" }}><Select options={[{ value: "short", label: "短" }, { value: "medium", label: "中" }, { value: "long", label: "长" }, { value: "unknown", label: "未知" }]} /></Form.Item><Form.Item name="rubric_complexity" label="细则复杂度" rules={[{ required: true }]} style={{ width: "34%" }}><Select options={[{ value: "low", label: "低" }, { value: "medium", label: "中" }, { value: "high", label: "高" }, { value: "unknown", label: "未知" }]} /></Form.Item></Space.Compact>
          <Space.Compact block><Form.Item name="reference_score" label="参考分" rules={[{ required: true }]} style={{ width: "33%" }}><InputNumber min={0} style={{ width: "100%" }} /></Form.Item><Form.Item name="model_score" label="模型分" rules={[{ required: true }]} style={{ width: "33%" }}><InputNumber min={0} style={{ width: "100%" }} /></Form.Item><Form.Item name="max_score" label="满分" rules={[{ required: true }]} style={{ width: "34%" }}><InputNumber min={0.1} style={{ width: "100%" }} /></Form.Item></Space.Compact>
          <Divider orientation="left">链路证据（按实际采集填写）</Divider>
          <Space.Compact block><Form.Item name="page_match_correct" label="页面匹配正确" style={{ width: "33%" }}><Select allowClear options={[{ value: true, label: "正确" }, { value: false, label: "错误" }]} /></Form.Item><Form.Item name="crop_iou" label="Crop IoU" style={{ width: "33%" }}><InputNumber min={0} max={1} step={0.01} style={{ width: "100%" }} /></Form.Item><Form.Item name="transcription_cer" label="转录 CER" style={{ width: "34%" }}><InputNumber min={0} max={1} step={0.01} style={{ width: "100%" }} /></Form.Item></Space.Compact>
          <Space.Compact block><Form.Item name="formula_exact" label="公式精确识别" style={{ width: "33%" }}><Select allowClear options={[{ value: true, label: "精确" }, { value: false, label: "错误" }]} /></Form.Item><Form.Item name="rubric_criterion_agreement" label="Rubric 评分点一致率" style={{ width: "33%" }}><InputNumber min={0} max={1} step={0.01} style={{ width: "100%" }} /></Form.Item><Form.Item name="error_source" label="首要错误来源" rules={[{ required: true }]} style={{ width: "34%" }}><Select options={errorSourceOptions} /></Form.Item></Space.Compact>
          <Space.Compact block><Form.Item name="needs_human_review" label="已路由人工" rules={[{ required: true }]} style={{ width: "33%" }}><Select options={[{ value: false, label: "否" }, { value: true, label: "是" }]} /></Form.Item><Form.Item name="reference_reviewer_count" label="真值复核人数" rules={[{ required: true }]} style={{ width: "33%" }}><InputNumber min={1} precision={0} style={{ width: "100%" }} /></Form.Item><Form.Item name="reference_adjudicated" label="已完成裁决" rules={[{ required: true }]} style={{ width: "34%" }}><Select options={[{ value: false, label: "否" }, { value: true, label: "是" }]} /></Form.Item></Space.Compact>
        </Form>
      </Drawer>

      <Drawer title="从已冻结评测创建校准" width={620} open={calibrationDrawerOpen} onClose={() => closeDraft(setCalibrationDrawerOpen)} extra={<Button type="primary" loading={actioning} onClick={() => void createCalibration()}>创建草稿</Button>}>
        <Form form={calibrationForm} layout="vertical">
          <Form.Item name="key" label="校准键" rules={[{ required: true, pattern: /^[a-z0-9][a-z0-9._-]{0,127}$/ }]}><Input placeholder="math-r2-confidence-v1" /></Form.Item>
          <Form.Item name="evaluation_run_id" label="已冻结评测" rules={[{ required: true }]}><Select options={evaluationRuns.filter((item) => item.status === "completed").map((item) => ({ value: item.id, label: `${item.display_name} · ${item.model_reference}` }))} /></Form.Item>
          <Form.Item name={["axis", "model_reference"]} label="模型版本" rules={[{ required: true }]}><Input /></Form.Item>
          <Space.Compact block><Form.Item name={["axis", "prompt_version"]} label="提示词版本" rules={[{ required: true }]} style={{ width: "50%" }}><Input /></Form.Item><Form.Item name={["axis", "rubric_version"]} label="评分细则版本" rules={[{ required: true }]} style={{ width: "50%" }}><Input /></Form.Item></Space.Compact>
          <Space.Compact block><Form.Item name={["axis", "subject"]} label="学科" rules={[{ required: true, pattern: /^[a-z0-9][a-z0-9._-]{0,127}$/ }]} style={{ width: "34%" }}><Input /></Form.Item><Form.Item name={["axis", "archetype"]} label="题型" rules={[{ required: true, pattern: /^[a-z0-9][a-z0-9._-]{0,127}$/ }]} style={{ width: "33%" }}><Input /></Form.Item><Form.Item name={["axis", "slice_key"]} label="切片" rules={[{ required: true }]} style={{ width: "33%" }}><Input placeholder="all" /></Form.Item></Space.Compact>
          <Form.Item name="method" label="校准方法" rules={[{ required: true }]}><Select options={["auto", "isotonic", "logistic", "conformal"].map((value) => ({ value, label: value }))} /></Form.Item>
        </Form>
      </Drawer>

      <Drawer title="人工分类人机分歧" width={520} open={classificationDrawerOpen} onClose={() => closeDraft(setClassificationDrawerOpen)} extra={<Button type="primary" loading={actioning} onClick={() => void submitClassification()}>保存分类</Button>}>
        <Alert showIcon type="info" message="分类仅记录诊断结论" description="不会自动更改学生成绩、重试 AI 或认定任何一方责任。" />
        <Form form={classificationForm} layout="vertical" style={{ marginTop: 16 }}><Form.Item name="taxonomy" label="根因分类" rules={[{ required: true }]}><Select options={taxonomyOptions} /></Form.Item><Form.Item name="notes" label="内部说明（可选）"><Input.TextArea rows={5} maxLength={2000} /></Form.Item></Form>
      </Drawer>

      <Drawer title="关联人工阅卷任务" width={520} open={routeDrawerOpen} onClose={() => closeDraft(setRouteDrawerOpen)} extra={<Button type="primary" loading={actioning} disabled={!routeTask} onClick={() => void submitRoute()}>关联任务</Button>}>
        <Alert showIcon type="warning" message="只能关联现有人工任务" description="该操作不会新建任务、不会直接重评，也不会修改最终成绩。需要更正时请进入正式重评流程。" />
        <Form form={routeForm} layout="vertical" style={{ marginTop: 16 }}><Form.Item name="review_task_id" hidden rules={[{ required: true }]}><Input /></Form.Item></Form>
        {routeDrawerOpen && selectedDisagreement ? <DisagreementTaskSelector key={`${tenantID}:${selectedDisagreement.id}`} tenantID={tenantID} disagreement={selectedDisagreement} disabled={actioning} onSelect={task => { setRouteTask(task); routeForm.setFieldsValue({ review_task_id: task?.id }); }} /> : null}
      </Drawer>
    </div>
  );
}
