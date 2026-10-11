-- 答题卡源文件继续复用现有版本和模板外键，但不得作为试卷资料计入准备检查。
-- 存量文件保留试卷用途，已有模板引用不变；只给今后独立上传的答题卡标记新用途。
ALTER TABLE exam_paper ADD COLUMN purpose TEXT NOT NULL DEFAULT 'question_paper'
  CHECK (purpose IN ('question_paper', 'answer_sheet'));
COMMENT ON COLUMN exam_paper.purpose IS 'question_paper 考试试卷；answer_sheet 独立上传的空白答题卡源文件';
