-- 未声明专用二维码区的旧模板，可在打印时确认可覆盖区域。
-- 每批次首次确认后冻结位置；NULL 保留旧记录，下载须经同一预检和人工确认恢复。
ALTER TABLE answer_sheet_print_batch
  ADD COLUMN print_layout JSONB,
  ADD CONSTRAINT answer_sheet_print_layout_object
    CHECK (print_layout IS NULL OR jsonb_typeof(print_layout)='object');
