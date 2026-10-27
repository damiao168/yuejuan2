import type { Dispatch, SetStateAction } from "react";
import type { UploadProps } from "antd";
import type { AnswerSheetTemplate } from "../../../api/configuration";
import type { PaperVersion, Question } from "../../../api/papers";
import type { useOMRCalibration } from "../calibration/useOMRCalibration";
import type { useTemplateBinding } from "../binding/useTemplateBinding";
import type { useStudentPrinting } from "../printing/useStudentPrinting";
import type { useTemplatePreview } from "../editor/useTemplatePreview";
import type { useTemplateEditor } from "../editor/useTemplateEditor";
import type { useAnswerSheetSourceUpload } from "../editor/useAnswerSheetSourceUpload";
import type { useAnswerSheetSourceDelete } from "../editor/useAnswerSheetSourceDelete";

// 子视图只消费本次渲染的状态与动作；保存、上传和请求代次仍由原控制器管理。
export type TemplatePageViewState = Readonly<{
	canManage: boolean;
	canCalibrate: boolean;
	papers: PaperVersion[];
	questions: Question[];
	templates: AnswerSheetTemplate[];
	selectedPaperId: string;
	setSelectedPaperId: Dispatch<SetStateAction<string>>;
	selectedTemplateId: string;
	setSelectedTemplateId: Dispatch<SetStateAction<string>>;
	selectedTemplate?: AnswerSheetTemplate;
  settingsOpen: boolean;
  setSettingsOpen: Dispatch<SetStateAction<boolean>>;
	templateDirty: boolean;
	canEditSources: boolean;
  sourceRecognitionBlocked?: boolean;
	uploadProps: UploadProps;
	confirmDiscardTemplate: (action: () => void) => void;
	loadData: () => Promise<void>;
	binding: ReturnType<typeof useTemplateBinding>;
	printing: ReturnType<typeof useStudentPrinting>;
	previewController: ReturnType<typeof useTemplatePreview>;
	calibration: ReturnType<typeof useOMRCalibration>;
	sourceUpload: ReturnType<typeof useAnswerSheetSourceUpload>;
  sourceManagement?: ReturnType<typeof useAnswerSheetSourceDelete>;
	editor: ReturnType<typeof useTemplateEditor>;
}>;

export type TemplatePageViewProps = Readonly<{ view: TemplatePageViewState }>;

// 组合入口保留完整 view；叶子面板只声明实际读取的状态/动作，不能借类型取得其他领域的写入能力。
type EditorFields<K extends keyof TemplatePageViewState["editor"]> = Readonly<{ editor: Pick<TemplatePageViewState["editor"], K> }>;
type PreviewFields<K extends keyof TemplatePageViewState["previewController"]> = Readonly<{ previewController: Pick<TemplatePageViewState["previewController"], K> }>;

export type TemplateSourceStatusView = Pick<TemplatePageViewState, "sourceUpload" | "sourceManagement" | "uploadProps" | "confirmDiscardTemplate">;

export type TemplateConfirmationView = Pick<TemplatePageViewState, "selectedTemplate" | "questions" | "canManage" | "canEditSources">
  & Readonly<{
    sourceUpload: Pick<TemplatePageViewState["sourceUpload"], "uploading">;
    printing: Pick<TemplatePageViewState["printing"], "printBusy">;
    binding: Pick<TemplatePageViewState["binding"], "examBinding" | "bindingBusy">;
  }>
  & EditorFields<"saving" | "suggestingRegions" | "readonly" | "objectiveGroups" | "reviewMessage" | "coveredQuestions">;

export type TemplateSourceToolbarView = TemplateSourceStatusView
  & Pick<TemplatePageViewState, "papers" | "selectedPaperId" | "selectedTemplate" | "setSelectedPaperId" | "setSelectedTemplateId" | "canManage" | "canEditSources" | "sourceRecognitionBlocked" | "setSettingsOpen">
  & EditorFields<"saving" | "suggestingRegions" | "setLayout" | "readonly" | "createDraft" | "cloneTemplate" | "suggestQuestionRegions" | "saveDraft" | "suggestionProgress" | "suggestionError" | "suggestionTone" | "suggestionModel" | "unassignedRegions" | "objectiveGroups" | "canUndo" | "undo" | "canRedo" | "redo" | "revision" | "layout" | "resetFillGradingGroups" | "discardSuggestedRegion" | "discardObjectiveGroup">
  & PreviewFields<"preview">
  & Readonly<{ binding: Pick<TemplatePageViewState["binding"], "bindingBusy">; printing: Pick<TemplatePageViewState["printing"], "printBusy"> }>;

export type TemplateQuestionPaneView = Pick<TemplatePageViewState, "questions">
  & EditorFields<"selectedQuestionId" | "setSelectedQuestionId" | "coveredQuestions" | "readonly" | "createRegionWithoutDragging" | "layout" | "setSelectedRegionId">
  & PreviewFields<"setPageNo">;

export type TemplateIdentityView = EditorFields<"layout" | "selectedRegionKind" | "selectedRegion" | "readonly" | "currentPage" | "createIdentityRegion" | "setSelectedRegionId" | "removeRegion" | "updateRegion">
  & PreviewFields<"setPageNo">;
export type TemplateFillGradingView = EditorFields<"fillGradingGroup" | "readonly" | "setFillGradingMode" | "setFillGradingRegion">
  & PreviewFields<"preview">;

export type TemplateCanvasView = Pick<TemplatePageViewState, "questions" | "selectedPaperId" | "selectedTemplate">
  & TemplateIdentityView & TemplateFillGradingView
  & PreviewFields<"preview" | "pageNo" | "setPageNo">
  & EditorFields<"coveredQuestions" | "layout" | "zoom" | "setZoom" | "canvasRef" | "readonly" | "currentPage" | "selectedQuestionId" | "selectedRegionId" | "draftRect" | "onCanvasPointerDown" | "onCanvasPointerMove" | "onCanvasPointerUp" | "beginRegionInteraction" | "unassignedRegions" | "objectiveGroups" | "cancelInteraction" | "selectedRegionKind" | "objectiveGrid" | "confirmPageRegions" | "fillGradingGroup" | "discardObjectiveGroup" | "assignSuggestedRegion" | "assignSuggestedIdentity" | "discardSuggestedRegion" | "setSelectedRegionId" | "setSelectedQuestionId">;

export type TemplateRegionEditorView = EditorFields<"selectedRegion" | "selectedQuestion" | "readonly" | "updateRegion" | "selectedRegionKind" | "addOptionRegion" | "updateOption" | "removeOptionRegion">;
export type TemplateBindingView = TemplateConfirmationView & EditorFields<"confirmActivate">
  & Readonly<{ binding: Pick<TemplatePageViewState["binding"], "examBinding" | "bindingBusy" | "releaseExamBinding" | "bindTemplateForExam"> }>;
export type TemplateOMRView = Pick<TemplatePageViewState, "selectedTemplate" | "questions" | "canCalibrate" | "calibration">
  & EditorFields<"layout" | "readonly" | "setOMRProfile">;
export type TemplatePrintingView = Pick<TemplatePageViewState, "selectedTemplate" | "canManage" | "printing">;
export type TemplateSettingsView = TemplateBindingView & TemplateRegionEditorView & TemplateOMRView & TemplatePrintingView
  & Pick<TemplatePageViewState, "settingsOpen" | "setSettingsOpen">
  & EditorFields<"name" | "setName" | "selectedRegionId" | "removeRegion" | "layout" | "coveredQuestions" | "confirmRegion">;
