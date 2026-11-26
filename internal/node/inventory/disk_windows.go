//go:build windows

package inventory

func statFilesystem(_ string) (total, used, free uint64, fsid string, ok bool) {
	return 0, 0, 0, "", false
}
