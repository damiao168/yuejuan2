# EduGrade 学生端

独立的学生成绩门户。它只调用学生范围的已发布成绩 API：

- `GET /api/v1/student/exams`
- `GET /api/v1/student/exams/{examId}/result`
- `GET /api/v1/student/exams/{examId}/questions/{questionId}`

不加载管理端、阅卷端或模型治理数据。开发时运行 `npm --workspace @edugrade/student-portal run dev`，默认端口为 `5174`。

首页使用 `/`，考试详情使用 `/exams/{examId}`。根入口的旧 `/#/exams/{examId}` 书签会自动转换为新地址；独立部署静态站点时，页面深路径需回落到 `index.html`，才能直接打开和刷新考试详情。
