import { expect, it } from "vitest";
import { parseGovernanceMaterial, scoringMaterialMatchesReport } from "./materialIntake";

const report = { key:"math-v1",display_name:"数学验证",model_reference:"model-v1",prompt_version:"prompt-v1",rubric_version:"rubric-v1",dataset_reference:"dataset-v1",dataset_sha256:"a".repeat(64) };
const observation = {response_key:"answer-1",response_fingerprint:"b".repeat(64),reference_kind:"gold",subject:"mathematics",archetype:"short_constructed",reference_score:3,model_score:2,max_score:5,ocr_quality:"high",answer_length:"short",rubric_complexity:"low",error_source:"none",needs_human_review:false,reference_reviewer_count:1,reference_adjudicated:false};
it("逐卷材料白名单复制来源与版本，不能复用到修改后的报告",()=>{
 const parsed=parseGovernanceMaterial(JSON.stringify({tenant_id:"untrusted-school",evaluation:{...report,status:"completed",tenant_id:"other"},observations:[{...observation,student_answer:"不能上传的原始文本"}]}));
 expect(parsed.kind).toBe("scoring_records");if(parsed.kind!=="scoring_records") throw new Error("wrong material");
 expect(parsed.material.evaluation).toEqual(report);
 expect(parsed.material.observations[0]).toEqual(observation);
 expect(scoringMaterialMatchesReport(parsed.material,report)).toBe(true);
 expect(scoringMaterialMatchesReport(parsed.material,{...report,key:"my-report",display_name:"便于识别的名称"})).toBe(true);
 for(const field of ["model_reference","prompt_version","rubric_version","dataset_reference","dataset_sha256"] as const) expect(scoringMaterialMatchesReport(parsed.material,{...report,[field]:"changed"})).toBe(false);
});
it("候选汇总从同一入口识别并保留数据集和每个候选的版本",()=>{
 const parsed=parseGovernanceMaterial(JSON.stringify({run_key:"summary",display_name:"汇总",dataset_reference:report.dataset_reference,dataset_sha256:report.dataset_sha256,evidence_class:"protocol_fixture",subject:"mathematics",grade:"九年级",question_type:"short_answer",modality:"text",sample_count:1,repeat_count:1,reason:"离线验证",candidates:[{model_config_id:"model-config",prompt_version:"prompt-v2",rubric_version:"rubric-v2",reason:"真实结果",evaluated_samples:1,teacher_reviewed_samples:0,teacher_accepted_samples:0,serious_error_samples:0,evidence_valid_samples:1,repeat_comparisons:0,stable_repeat_samples:0,p95_latency_ms:1,total_cost_micros:0}]}));
 expect(parsed.kind).toBe("candidate_summary");if(parsed.kind!=="candidate_summary") throw new Error("wrong material");
 expect(parsed.material.run.dataset_sha256).toBe(report.dataset_sha256);
 expect(parsed.material.candidates[0]).toMatchObject({prompt_version:"prompt-v2",rubric_version:"rubric-v2"});
});
it("逐卷材料拒绝不带来源版本的裸数组和重复答卷，不能发布部分预览",()=>{
 expect(()=>parseGovernanceMaterial(JSON.stringify([observation]))).toThrow(/evaluation/);
 expect(()=>parseGovernanceMaterial(JSON.stringify({evaluation:report,observations:[observation,observation]}))).toThrow(/答卷键须唯一/);
 expect(()=>parseGovernanceMaterial(JSON.stringify({evaluation:{...report,prompt_version:""},observations:[observation]}))).toThrow(/prompt_version/);
});
