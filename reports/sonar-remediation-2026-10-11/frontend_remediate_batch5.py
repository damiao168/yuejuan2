from pathlib import Path
replacements={
'apps/desktop-client/src/components/OfflineWorkbench.tsx': [('!draft || draft.score === null','draft?.score == null')],
'apps/desktop-client/src/features/capture/useExamCaptureContext.ts':[('!focused || focused.examId !== examID','focused?.examId !== examID')],
'apps/web-admin/src/features/grading/workbench/components/FastConfirmDrawer.tsx':[('!candidate || candidate.grade.id !== item.candidate.grade.id','candidate?.grade.id !== item.candidate.grade.id')],
'apps/web-admin/src/features/grading/workbench/hooks/useGradingMathActions.ts':[('!ctx || activeTask.current !== ctx.task.id','activeTask.current !== ctx?.task.id')],
'apps/web-admin/src/features/grading/workbench/hooks/useGradingReviewActions.ts':[('!ctx || activeTask.current !== ctx.task.id','activeTask.current !== ctx?.task.id')],
'apps/web-admin/src/features/question-bank/QuestionBankPage.tsx':[('!latestPreview || latestPreview.id !== loadedVersion.current','latestPreview?.id !== loadedVersion.current')],
'apps/web-admin/src/features/score-management/hooks/useRegradeWorkflow.tsx':[('!regradePreview || regradePreview.question_id !== regradeQuestionId','regradePreview?.question_id !== regradeQuestionId')],
'apps/web-admin/src/pages/AppealCenterPage.tsx':[('!selected || selected.id !== selectedIdRef.current','selected?.id !== selectedIdRef.current'),('!selected ||\n      selected.id !== selectedIdRef.current','selected?.id !== selectedIdRef.current')],
'apps/web-admin/src/pages/CaptureBatchPage.tsx':[('Boolean(detail && detail.batch.id === selectedId)','detail?.batch.id === selectedId'),('!detail || detail.batch.id !== selectedId','detail?.batch.id !== selectedId')],
'apps/web-admin/src/pages/ExamPreparationPage.tsx':[('!exam || exam.id !== currentExamId.current','exam?.id !== currentExamId.current')],
'apps/web-admin/src/pages/PaperRubricPage.tsx':[('!receipt || receipt.owner !== activeSaveOwner.current','receipt?.owner !== activeSaveOwner.current')],
'apps/web-admin/src/pages/SubmissionCapturePage.tsx':[('!latest || latest.row.submission.id !== current.row.submission.id','latest?.row.submission.id !== current.row.submission.id')],
'apps/web-admin/src/features/exams/management/ManagementExamArchiveDialog.tsx':[('if (!pending) pending = createSubmission(exam, selection.action);','pending ??= createSubmission(exam, selection.action);')],
'apps/web-admin/src/features/grading/workbench/components/MathRubricMatrix.tsx':[('score.suggested_score === null ? `${score.score_range.min}–${score.score_range.max}` : score.suggested_score','score.suggested_score ?? `${score.score_range.min}–${score.score_range.max}`')],
}
for f,rs in replacements.items():
 p=Path(f);s=p.read_text(encoding='utf-8')
 for old,new in rs:
  assert old in s,(f,old);s=s.replace(old,new)
 p.write_text(s,encoding='utf-8')
