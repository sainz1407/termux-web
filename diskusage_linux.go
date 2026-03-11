//go:build linux

package main

import "syscall"

// diskUsage uses the statfs syscall (same as Python's psutil.disk_usage).
// This is far more reliable than parsing `df` output on Termux.
func diskUsage(path string) (total, used, free int64, err error) {
	var stat syscall.Statfs_t
	err = syscall.Statfs(path, &stat)
	if err != nil {
		return
	}
	bsize := int64(stat.Frsize)
	if bsize == 0 {
		bsize = int64(stat.Bsize)
	}
	total = int64(stat.Blocks) * bsize
	free = int64(stat.Bavail) * bsize // available to unprivileged user
	used = total - free
	return
}
