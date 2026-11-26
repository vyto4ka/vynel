//go:build !windows

package inventory

import (
	"fmt"
	"syscall"
)

func statFilesystem(path string) (total, used, free uint64, fsid string, ok bool) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil || st.Bsize <= 0 {
		return 0, 0, 0, "", false
	}
	bsize := uint64(st.Bsize)
	total = st.Blocks * bsize
	free = st.Bavail * bsize
	if st.Bfree <= st.Blocks {
		used = total - st.Bfree*bsize
	}
	return total, used, free, fmt.Sprintf("%v", st.Fsid), true
}
