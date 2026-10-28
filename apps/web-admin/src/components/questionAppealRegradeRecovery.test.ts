import { expect,it } from "vitest";
import type { RegradeJob } from "../api/scoreReleases";
import type { PublishedQuestionAppeal } from "../api/questionAppeals";
import { matchingAppealRegradeDrafts } from "./questionAppealRegradeRecovery";

it("只回认当前账号同来源单份人工复评，旧随机幂等键不参与判断",()=>{
 const appeal={exam_id:"exam",question_id:"q",source_release_id:"release",submission_id:"submission"} as PublishedQuestionAppeal;
 const job={id:"old-random-key-job",status:"draft",created_by:"actor",exam_id:"exam",question_id:"q",source_release_id:"release",reason_code:"appeal_pattern",reason_text:"核对证据",strategy:"human_recheck",affected_count:1,selector:{submission_ids:["submission"]},severity_delta:0} as RegradeJob;
 expect(matchingAppealRegradeDrafts([job],appeal,"actor","核对证据")).toEqual([job]);
 for(const changed of [{created_by:"other"},{source_release_id:"other"},{reason_text:"另一项原因"},{status:"approved"},{affected_count:2},{selector:{submission_ids:["submission","other"]}},{selector:{}},{new_policy_version:"new"},{strategy:"ai_regrade"}])expect(matchingAppealRegradeDrafts([{...job,...changed} as RegradeJob],appeal,"actor","核对证据")).toEqual([]);
});
