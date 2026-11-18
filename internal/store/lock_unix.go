//go:build unix

package store

import (
	"os"
	"syscall"
)

// Lock 取得数据目录的独占锁，返回解锁函数。
// 改动期望配置或容器状态的操作都在锁内进行，避免两个管理器进程同时接线。
func (s *Store) Lock() (unlock func(), err error) {
	f, err := os.OpenFile(s.path("lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, err
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}, nil
}
