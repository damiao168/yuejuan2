-- 000092 的事件类型约束漏了 review_opinion_submitted；复核教师提交意见时写该事件，数据库一直拒绝，提交失败。
-- 写事件和更新申诉修订在同一事务内，失败时整体回滚，库里没有半写入的申诉或事件，所以只放宽约束，不需要修补存量。
-- 修正后 000218 的私有草稿清理触发器才会在提交意见时真正执行。
-- 允许值必须与申诉存储实际写入的事件一致，由 internal/appeal/published_question_event_type_postgres_test.go 核对。
ALTER TABLE question_appeal_event DROP CONSTRAINT IF EXISTS question_appeal_event_event_type_check;
ALTER TABLE question_appeal_event ADD CONSTRAINT question_appeal_event_event_type_check
  CHECK (event_type IN ('submitted','review_started','review_opinion_submitted','decided','resolved'));
