import { useState } from "react";
import { Button, Select } from "antd";
import { Plus, Trash2 } from "lucide-react";
import { IDENTITY_REGION_LABELS, normalizeIdentityRegionLabel } from "../editor/identityRegionFields";
import type { TemplatePageViewProps } from "./templateViewTypes";

export function IdentityRegionPanel({ view }: TemplatePageViewProps) {
  const { editor, previewController } = view;
  const [newLabel, setNewLabel] = useState("学号");
  const regions = editor.layout.pages.flatMap(page => page.identity_regions.map(region => ({ pageNo: page.page_no, region })));
  const options = IDENTITY_REGION_LABELS.map(label => ({ value: label, label }));
  const selectedIdentity = editor.selectedRegionKind === "identity" ? editor.selectedRegion : undefined;
  return <section className="template-option-editor template-identity-editor" aria-label="个人信息区域">
    <div className="template-pane-head"><strong>个人信息区域</strong><span>{regions.length}</span></div>
    <div className="template-identity-controls">
      <Select aria-label="新增个人信息字段" value={newLabel} options={options} disabled={editor.readonly} onChange={setNewLabel} />
      <Button icon={<Plus size={14} />} aria-label="添加个人信息区域" disabled={editor.readonly || !editor.currentPage}
        onClick={() => editor.createIdentityRegion(newLabel)}>添加</Button>
    </div>
    <div className="template-identity-list">
      {regions.map(({ pageNo, region }) => <div key={region.id}
        className={`template-identity-row${selectedIdentity?.id === region.id ? " is-selected" : ""}`}>
        <Button type="text" aria-label={`定位第 ${pageNo} 页${normalizeIdentityRegionLabel(region.label)}区域`}
          aria-pressed={selectedIdentity?.id === region.id} onClick={() => {
            previewController.setPageNo(pageNo); editor.setSelectedRegionId(region.id);
            requestAnimationFrame(() => [...document.querySelectorAll<HTMLElement>(".template-identity-region[data-region-id]")]
              .find(element => element.dataset.regionId === region.id)?.scrollIntoView?.({ block: "center", inline: "center" }));
          }}><span>{normalizeIdentityRegionLabel(region.label)}</span><small>第 {pageNo} 页</small></Button>
        <Button type="text" danger icon={<Trash2 size={13} />} aria-label={`删除第 ${pageNo} 页${normalizeIdentityRegionLabel(region.label)}区域`}
          disabled={editor.readonly} onClick={() => editor.removeRegion(region.id)} />
      </div>)}
      {!regions.length ? <p className="muted">尚未配置</p> : null}
    </div>
    {selectedIdentity ? <label className="template-identity-field"><span>字段类型</span>
      {/* 模板只保存字段类别；学生填写的姓名、号码不进入可复用的模板标签。 */}
      <Select aria-label="个人信息字段类型" value={normalizeIdentityRegionLabel(selectedIdentity.label)} options={options}
        disabled={editor.readonly} onChange={label => editor.updateRegion(selectedIdentity.id, { label })} />
    </label> : null}
  </section>;
}
