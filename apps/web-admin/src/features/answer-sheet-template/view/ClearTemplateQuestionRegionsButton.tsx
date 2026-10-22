import { useEffect, useRef, type Dispatch, type SetStateAction } from "react";
import { App, Button } from "antd";
import { Eraser } from "lucide-react";
import type { LayoutRegion, TemplateLayout } from "../../../api/configuration";

type ClearQuestionRegionsProps = Readonly<{
  scopeKey: string;
  editable: boolean;
  busy: boolean;
  layout: TemplateLayout;
  unassignedRegions: Map<number, LayoutRegion[]>;
  setLayout: Dispatch<SetStateAction<TemplateLayout>>;
  discardSuggestedRegion: (candidateId: string) => void;
  onCleared?: () => void;
}>;

/** 只清理本地草稿题区；后台保存版本和页面定位、身份区域均不参与本次变更。 */
export function ClearTemplateQuestionRegionsButton(props: ClearQuestionRegionsProps) {
  const { modal } = App.useApp();
  const mounted = useRef(true);
  const latest = useRef(props);
  latest.current = props;
  const boundary = `${props.scopeKey}:${props.editable}:${props.busy}`;
  const guard = useRef({ boundary, generation: 0 });
  if (guard.current.boundary !== boundary) guard.current = { boundary, generation: guard.current.generation + 1 };
  useEffect(() => {
    mounted.current = true;
    return () => { mounted.current = false; guard.current.generation += 1; };
  }, []);
  const hasRegions = props.layout.pages.some(page => page.question_regions.length || page.grading_groups?.length)
    || [...props.unassignedRegions.values()].some(regions => regions.length);
  const confirmClear = () => {
    if (!props.editable || props.busy || !hasRegions) return;
    const generation = guard.current.generation;
    let consumed = false;
    const isCurrent = () => mounted.current && generation === guard.current.generation
      && latest.current.editable && !latest.current.busy;
    modal.confirm({
      title: "清空当前草稿的旧题区？",
      content: "将清空整份答题卡所有页面的题目区域和未关联候选，保留定位区及身份区，不修改后台已保存版本。清空后请再点击“识别区域”。",
      okText: "清空旧题区", cancelText: "保留现有题区", okButtonProps: { danger: true },
      onCancel: () => { consumed = true; },
      onOk: () => {
        // 确认弹窗可晚于来源、模板或权限切换；旧确认不能操作新的草稿，即使后来又切回原来源。
        if (consumed || !isCurrent()) return;
        consumed = true;
        const current = latest.current;
        current.onCleared?.();
        const candidateIDs = [...current.unassignedRegions.values()].flatMap(regions => regions.map(region => region.id));
        current.setLayout(layout => isCurrent()
          ? { ...layout, pages: layout.pages.map(page => ({ ...page, question_regions: [], ...(page.grading_groups ? { grading_groups: [] } : {}) })) }
          : layout);
        for (const candidateID of candidateIDs) current.discardSuggestedRegion(candidateID);
      }
    });
  };
  return <Button danger icon={<Eraser size={16} />} disabled={!props.editable || props.busy || !hasRegions}
    onClick={confirmClear}>清空旧题区</Button>;
}
