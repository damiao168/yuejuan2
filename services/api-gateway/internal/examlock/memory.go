// Package examlock 为内存存储提供与 PostgreSQL 考试咨询锁对应的临界区。
package examlock

import (
	"hash/fnv"
	"sync"
)

var stripes [128]sync.Mutex

// Memory 必须先于各存储自身的锁取得；释放前不能创建同考试的新发布阻断事实。
// 固定分片避免为测试中不断变化的考试标识永久保留锁对象。
func Memory(tenantID, examID string) func() {
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(tenantID + "\x00" + examID))
	lock := &stripes[hash.Sum32()%uint32(len(stripes))]
	lock.Lock()
	return lock.Unlock
}
