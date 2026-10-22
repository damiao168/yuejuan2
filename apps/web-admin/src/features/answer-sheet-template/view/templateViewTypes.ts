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
