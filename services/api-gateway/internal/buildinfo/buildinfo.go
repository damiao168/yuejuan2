package buildinfo

// 发布构建通过 -ldflags 替换这些值；开发二进制保留明确的本地值，不冒充发布版本。
var (
	Version       = "dev"
	GitSHA        = "unknown"
	BuildTime     = "unknown"
	ImageDigest   = "unknown"
	ReleaseID     = "local"
	SchemaVersion = "unknown"
)

func Public() map[string]string {
	return map[string]string{
		"version": Version, "git_sha": GitSHA, "build_time": BuildTime,
		"image_digest": ImageDigest, "release_id": ReleaseID, "schema_version": SchemaVersion,
	}
}
