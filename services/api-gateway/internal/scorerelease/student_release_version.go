package scorerelease

// requireStudentRelease 绑定实际读取的公开证据与页面版本；省略参数保留旧客户端行为。
// 不另查当前指针，即使读取完成后发布切换，已读取的证据也仍属于原版本。
func requireStudentRelease(actual string, expected []string) error {
	if len(expected) > 0 && expected[0] != "" && actual != expected[0] {
		return ErrStudentReleaseChanged
	}
	return nil
}
