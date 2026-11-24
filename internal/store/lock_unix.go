//go:build unix

package store

import (
	"errors"
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

// LockSampler 试着取得“由我来写流量历史”的独占权，不等待：同一个数据目录上开着网页又开着 watch 时，
// 只有先拿到的那个进程落盘，另一个只在内存里看实时的数。ok 为假表示已经有别的进程在写。
// 拿到之后一直握到 unlock（通常是进程退出）。
func (s *Store) LockSampler() (unlock func(), ok bool, err error) {
	if err := os.MkdirAll(s.path(historyDir), 0o700); err != nil {
		return nil, false, err
	}
	f, err := os.OpenFile(s.path(historyDir, "sampler.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, false, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, false, nil
		}
		return nil, false, err
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}, true, nil
}
