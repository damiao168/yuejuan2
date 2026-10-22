import { Alert, Button, Checkbox, Drawer, InputNumber, Select, Space } from "antd";
import type { useObjectiveGrid } from "../editor/useObjectiveGrid";
import { ObjectiveGridCanvas } from "./ObjectiveGridCanvas";

export function ObjectiveGridDrawer({ grid, imageURL, pageNo, readonly }: {
  grid: ReturnType<typeof useObjectiveGrid>; imageURL?: string; pageNo: number; readonly: boolean;
}) {
  const busy = grid.busy || grid.ocrBusy || grid.applying;
  const disabled = readonly || busy;
  const options = grid.choiceQuestions.map(question => ({ value: question.id, label: `第 ${question.question_no} 题` }));
  const selectedOptions = options.filter(option => grid.questionIds.includes(option.value));
  const assignedIDs = new Set(Object.values(grid.assignments));
  const unassigned = selectedOptions.filter(option => !assignedIDs.has(option.value));
  const missingCount = Math.max(0, grid.questionIds.length - grid.groups.length);
  const labels = Object.fromEntries(Object.entries(grid.assignments).map(([groupID, questionID]) => [groupID,
    `第 ${grid.choiceQuestions.find(question => question.id === questionID)?.question_no ?? "?"} 题`]));
  return <Drawer title={`选择题填涂区设置 · 第 ${pageNo} 页`} width="min(1120px, 100vw)" open={grid.open} onClose={grid.closeGrid}
    extra={<Button type="primary" disabled={disabled || !grid.reviewed || (grid.partialDetection && !grid.partialReviewed) || Boolean(grid.validationError) || !grid.analysis} onClick={grid.apply}>应用到草稿</Button>}>
    <Space direction="vertical" size="middle" style={{ width: "100%" }}>
      <Alert type="info" showIcon message="框住一个完整的选择题大区，选择本区全部题目，再定位填涂格"
        description="“定位填涂格”查找每题 A/B/C/D 的实际位置；“识别题号”只读数字供核对，不会补格或自动关联。请核对题数和框的位置，漏检题目需另行补框。不同选项数的题目请分区处理。" />
      <label>本大区包含题目（含未识别到的题）
        <Select aria-label="本大区题目" mode="multiple" value={grid.questionIds} disabled={disabled} style={{ width: "100%" }}
          options={options} onChange={grid.changeQuestionIds} placeholder="对照原图选择本区全部题号，用于核对是否漏题" />
      </label>
      <Space wrap>
        <label>每题选项数 <InputNumber aria-label="每题选项数" min={2} max={12} precision={0} value={grid.optionCount} disabled={disabled}
          onChange={value => grid.changeOptionCount(Number(value ?? 4))} /></label>
        <label>选项排列 <Select aria-label="选项排列" value={grid.orientation} disabled={disabled} style={{ width: 130 }}
          options={[{ value: "horizontal", label: "左右排列" }, { value: "vertical", label: "上下排列" }]} onChange={grid.changeOrientation} /></label>
        <Button type="primary" loading={grid.busy} disabled={readonly || grid.ocrBusy || !grid.block} onClick={() => void grid.analyze()}>定位填涂格</Button>
        <Button loading={grid.ocrBusy} disabled={readonly || grid.busy || !grid.block} onClick={() => void grid.recognizeNumberHints()}>识别题号（参考）</Button>
        <Button disabled={disabled || !grid.block} onClick={() => grid.changeBlock(undefined)}>重画大区</Button>
      </Space>
      {grid.error ? <Alert type="warning" showIcon message={grid.error} /> : null}
      {grid.numberHints ? <Alert type="info" showIcon message={grid.numberHints} /> : null}
      <div style={{ display: "grid", gridTemplateColumns: "minmax(0, 1fr)", gap: 16 }}>
        {imageURL ? <ObjectiveGridCanvas imageURL={imageURL} block={grid.block} groups={grid.groups} orientation={grid.orientation}
          analysisKey={grid.analysis?.analysis.request_id} reverse={grid.reverse} labels={labels} disabled={disabled} onBlock={grid.changeBlock} /> : <Alert type="warning" message="当前页图像尚未准备好" />}
        {grid.analysis ? <section aria-label="选择题网格人工核对">
          <p>本区已选 {grid.questionIds.length} 题，定位到 {grid.groups.length} 组填涂格，每组 {grid.optionCount} 个选项。请逐组检查绿色框只覆盖真实填涂点。</p>
          {grid.partialDetection ? <Alert type="warning" showIcon message={missingCount ? `少定位到 ${missingCount} 题，不能按顺序关联题号` : "填涂格数量或位置仍需核对"}
            description="漏检会让后续题号错位。请逐组对照原图选择题号；本次只能应用核对过的题组，漏检题目需另行补框。识别题号不能修复漏格。" /> : null}
          {grid.validationError ? <Alert type="warning" showIcon message={grid.validationError} /> : null}
          <Space direction="vertical" size="middle" style={{ width: "100%" }}>
            <Space wrap>
              <label>题目阅读顺序 <Select aria-label="题目阅读顺序" value={grid.readOrder} disabled={disabled} style={{ width: 160 }}
                options={[{ value: "column_first", label: "先列后行" }, { value: "row_first", label: "先行后列" }]} onChange={grid.changeReadOrder} /></label>
              <Checkbox checked={grid.reverse} disabled={disabled} onChange={event => grid.changeReverse(event.target.checked)}>反向分配 A 至 {String.fromCharCode(64 + grid.optionCount)}</Checkbox>
            </Space>
            <Button disabled={disabled || grid.partialDetection || grid.questionIds.length !== grid.groups.length || !grid.groups.length} onClick={grid.mapSelectedQuestions}>按所选题号与阅读顺序关联</Button>
            <p>批量关联按上方所选题号的先后顺序依次对应画布中的题组；发现缺格时请逐组选择题号。</p>
            {grid.groups.map((group, index) => <Space key={group.group_id} wrap>
              <span>题组 {index + 1} · {group.options.length} 个选项</span>
              <Select aria-label={`题组 ${index + 1} 对应题目`} style={{ width: 180 }} allowClear showSearch optionFilterProp="label"
                value={grid.assignments[group.group_id] || undefined} options={selectedOptions} disabled={disabled || !grid.questionIds.length}
                onChange={value => grid.assignQuestion(group.group_id, value ?? "")} placeholder="核对本场题号" />
            </Space>)}
            {unassigned.length ? <p>尚未关联：{unassigned.map(option => option.label).join("、")}。请核对是否漏格。</p> : null}
            <Checkbox checked={grid.reviewed} disabled={disabled || Boolean(grid.validationError)} onChange={event => grid.setReviewed(event.target.checked)}>
              已逐组核对题号、题目顺序、选项数量及 A/B/C/D 方向，确认每个选项框对应一个真实填涂点
            </Checkbox>
            {grid.partialDetection ? <Checkbox checked={grid.partialReviewed} disabled={disabled || Boolean(grid.validationError)} onChange={event => grid.setPartialReviewed(event.target.checked)}>
              本次仅应用已核对的完整题组，漏检题目另行补框
            </Checkbox> : null}
          </Space>
        </section> : null}
      </div>
    </Space>
  </Drawer>;
}
