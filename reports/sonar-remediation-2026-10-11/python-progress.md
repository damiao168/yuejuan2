# Python 原Sonar问题处理记录

190 条全部跟踪：165 条已修改并绑定当前源码版本通过本地验证；期间3个外部修改文件已补验，其历史SHA变化证据保留。22 条有逐key误报证据等待平台处置，1 条本机架构预览 HTTP 待平台按上下文接受，2 条基线已含 TLS 策略等待实际复扫。没有使用 NOSONAR、源码排除或规则阈值放宽。

全部原问题的源码处理与上下文评审完成，Sonar实际复扫/关闭由主智能体统一执行；本记录不将其记为平台清零。完整逐文件维护审查与问题范围不同，未全文审读的范围明确为pending。

现有8组测试：1006个测试与217个subtests通过；首次OCR导入模式收集失败的日志已保留，正常prepend模式重跑388个测试通过。14个生成器在相同缓存输入下运行，所有42个生成写入只在内存捕获，输出等价；没有覆盖工作区生成产物。RLS新增单次扫描器通过12个旧正则语义兼容样例及10000个缺失END LOOP边界输入检查。64个原问题文件通过内存语法编译。

| 文件 | 原问题数 | 原问题处理状态 | 全文件审查状态 |
| --- | ---: | --- | --- |
| `ai-services/grading_agent/paper_compact/anchors.py` | 2 | reviewed_false_positive_pending_platform:2 | partial |
| `ai-services/grading_agent/paper_compact/layout.py` | 1 | reviewed_false_positive_pending_platform:1 | partial |
| `ai-services/grading_agent/paper_validation.py` | 1 | reviewed_false_positive_pending_platform:1 | partial |
| `ai-services/grading_agent/paper_visual.py` | 1 | reviewed_false_positive_pending_platform:1 | partial |
| `ai-services/grading_agent/server.py` | 1 | reviewed_tls_remediation_pending_rescan:1 | partial |
| `ai-services/tests/test_application.py` | 1 | validated_fix_pending_rescan:1 | partial |
| `ai-services/tests/test_application_v2.py` | 3 | validated_fix_pending_rescan:3 | partial |
| `ai-services/tests/test_contract.py` | 1 | validated_fix_pending_rescan:1 | partial |
| `ai-services/tests/test_contract_v2.py` | 2 | validated_fix_pending_rescan:2 | partial |
| `ai-services/tests/test_dashscope_multimodal_contract.py` | 5 | validated_fix_pending_rescan:5 | partial |
| `ai-services/tests/test_dashscope_native_transport.py` | 6 | validated_fix_pending_rescan:6 | partial |
| `ai-services/tests/test_managed_model.py` | 1 | validated_fix_pending_rescan:1 | partial |
| `ai-services/tests/test_math_rubric_draft.py` | 4 | validated_fix_pending_rescan:4 | partial |
| `ai-services/tests/test_model.py` | 7 | validated_fix_pending_rescan:7 | partial |
| `ai-services/tests/test_paper_input.py` | 2 | validated_fix_pending_rescan:2 | partial |
| `ai-services/tests/test_paper_parser.py` | 10 | validated_fix_pending_rescan:10 | partial |
| `ai-services/tests/test_paper_validation.py` | 2 | validated_fix_pending_rescan:2 | partial |
| `ai-services/tests/test_paper_visual.py` | 2 | validated_fix_pending_rescan:2 | partial |
| `ai-services/tests/test_provider_adapter.py` | 2 | validated_fix_pending_rescan:2 | partial |
| `docs/architecture/tools/build_database_specs.py` | 4 | validated_fix_pending_rescan:4 | partial |
| `docs/architecture/tools/build_diagrams.py` | 4 | validated_fix_pending_rescan:4 | partial |
| `docs/architecture/tools/build_module_catalog.py` | 1 | validated_fix_pending_rescan:1 | partial |
| `docs/architecture/tools/extract_database_catalog.py` | 4 | validated_fix_pending_rescan:4 | partial |
| `docs/architecture/tools/merge_review_ledger.py` | 4 | validated_fix_pending_rescan:4 | partial |
| `docs/architecture/tools/preview_server.py` | 2 | validated_fix_pending_rescan:1; reviewed_intentional_local_http_pending_platform:1 | annotated |
| `docs/architecture/tools/route_obstructions.py` | 1 | validated_fix_pending_rescan:1 | partial |
| `docs/architecture/tools/scan_repository.py` | 2 | validated_fix_pending_rescan:2 | partial |
| `docs/architecture/tools/simplify_diagrams.py` | 6 | validated_fix_pending_rescan:6 | partial |
| `docs/architecture/tools/verify_delivery.py` | 5 | validated_fix_pending_rescan:5 | partial |
| `docs/architecture/tools/write_frontend_audit.py` | 10 | validated_fix_pending_rescan:10 | partial |
| `docs/architecture/tools/write_frontend_specs.py` | 14 | validated_fix_pending_rescan:14 | partial |
| `docs/architecture/tools/write_root_review.py` | 6 | validated_fix_pending_rescan:6 | partial |
| `docs/architecture/tools/write_root_specs.py` | 4 | validated_fix_pending_rescan:4 | partial |
| `lab/evals/math-recognition/test_math_batch28.py` | 1 | validated_fix_pending_rescan:1 | partial |
| `lab/evals/math-recognition/test_math_bench.py` | 2 | validated_fix_pending_rescan:2 | partial |
| `scripts/generate-fujian-2024-math-simulation.py` | 6 | reviewed_false_positive_pending_platform:6 | partial |
| `services/image-quality-worker/image_quality/geometry.py` | 1 | reviewed_false_positive_pending_platform:1 | partial |
| `services/math-verification-worker/math_verification_worker/server.py` | 1 | reviewed_tls_remediation_pending_rescan:1 | partial |
| `services/ocr-worker/ocr_worker/formula_validation.py` | 1 | reviewed_false_positive_pending_platform:1 | partial |
| `services/ocr-worker/ocr_worker/math_layout.py` | 1 | reviewed_false_positive_pending_platform:1 | partial |
| `services/ocr-worker/ocr_worker/paper_formula.py` | 1 | reviewed_false_positive_pending_platform:1 | partial |
| `services/ocr-worker/ocr_worker/symbolic_runner.py` | 2 | reviewed_false_positive_pending_platform:2 | partial |
| `services/ocr-worker/tests/test_api_batch51.py` | 2 | validated_fix_pending_rescan:2 | partial |
| `services/ocr-worker/tests/test_benchmark.py` | 2 | validated_fix_pending_rescan:2 | partial |
| `services/ocr-worker/tests/test_formula_calibration.py` | 1 | validated_fix_pending_rescan:1 | partial |
| `services/ocr-worker/tests/test_http_response.py` | 1 | validated_fix_pending_rescan:1 | partial |
| `services/ocr-worker/tests/test_ocr_artifacts_batch39.py` | 3 | validated_fix_pending_rescan:3 | partial |
| `services/ocr-worker/tests/test_ocr_workflows_batch37.py` | 4 | validated_fix_pending_rescan:4 | partial |
| `services/ocr-worker/tests/test_paper_formula.py` | 4 | validated_fix_pending_rescan:4 | partial |
| `services/ocr-worker/tests/test_paper_formula_batch35.py` | 3 | validated_fix_pending_rescan:3 | partial |
| `services/ocr-worker/tests/test_recognition_router.py` | 4 | validated_fix_pending_rescan:4 | partial |
| `services/ocr-worker/tests/test_recognition_router_batch32.py` | 2 | validated_fix_pending_rescan:2 | partial |
| `services/ocr-worker/tests/test_startup_batch36.py` | 1 | reviewed_false_positive_pending_platform:1 | partial |
| `services/ocr-worker/tests/test_symbolic_runner.py` | 1 | validated_fix_pending_rescan:1 | partial |
| `services/page-processing-worker/page_processing/config.py` | 1 | reviewed_false_positive_pending_platform:1 | partial |
| `services/page-processing-worker/page_processing/omr.py` | 1 | reviewed_false_positive_pending_platform:1 | partial |
| `services/page-processing-worker/page_processing/omr_acceptance.py` | 1 | reviewed_false_positive_pending_platform:1 | partial |
| `services/page-processing-worker/page_processing/runner.py` | 1 | reviewed_false_positive_pending_platform:1 | partial |
| `services/page-processing-worker/tests/test_decoder.py` | 6 | validated_fix_pending_rescan:6 | partial |
| `services/page-processing-worker/tests/test_omr.py` | 6 | validated_fix_pending_rescan:6 | partial |
| `services/page-processing-worker/tests/test_registration.py` | 1 | validated_fix_pending_rescan:1 | partial |
| `services/page-processing-worker/tests/test_runner_state_boundaries.py` | 5 | validated_fix_pending_rescan:5 | partial |
| `services/subjective-grading-worker/tests/test_api_integration.py` | 1 | validated_fix_pending_rescan:1 | partial |
| `tests/ai-evaluation/test_evaluate.py` | 2 | validated_fix_pending_rescan:2 | partial |

详细 key、源码 SHA 与每条验证依据见 python-progress.json；非源码处置理由与摘录见 python-nonfix-evidence.json。
