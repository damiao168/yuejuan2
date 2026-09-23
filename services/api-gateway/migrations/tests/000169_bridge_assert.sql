DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM model_evaluation_candidate
    WHERE id = '66666666-6666-4666-8666-666666666666'
      AND managed_model_api_config_id = '44444444-4444-4444-8444-444444444444'
      AND deployment_id = '33333333-3333-4333-8333-333333333333'
      AND model_name = 'qwen-test'
  ) THEN
    RAISE EXCEPTION 'candidate bridge backfill failed';
  END IF;
  IF NOT EXISTS (
    SELECT 1 FROM model_approval
    WHERE id = '77777777-7777-4777-8777-777777777777'
      AND managed_model_api_config_id = '44444444-4444-4444-8444-444444444444'
      AND deployment_id = '33333333-3333-4333-8333-333333333333'
      AND model_name = 'qwen-test'
  ) THEN
    RAISE EXCEPTION 'approval bridge backfill failed';
  END IF;
  IF NOT EXISTS (
    SELECT 1 FROM model_evaluation_candidate
    WHERE id = '66666666-6666-4666-8666-666666666667'
      AND managed_model_api_config_id IS NULL
      AND deployment_id = '33333333-3333-4333-8333-333333333334'
      AND model_name = 'local-model'
  ) THEN
    RAISE EXCEPTION 'unmatched legacy candidate was fabricated or lost';
  END IF;
  IF NOT EXISTS (
    SELECT 1 FROM pg_trigger
    WHERE tgname = 'trg_model_approval_immutable' AND tgenabled = 'O'
  ) THEN
    RAISE EXCEPTION 'approval immutable trigger was not restored';
  END IF;
END;
$$;
