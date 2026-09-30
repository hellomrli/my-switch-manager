package library

import (
	"fmt"
	"os"
	"syscall"
)

func fileIdentity(f *os.File, _ os.FileInfo) string {
	var info syscall.ByHandleFileInformation
	if err := syscall.GetFileInformationByHandle(syscall.Handle(f.Fd()), &info); err == nil {
		return fmt.Sprintf("%d:%d:%d", info.VolumeSerialNumber, info.FileIndexHigh, info.FileIndexLow)
	}
	return ""
}

// Windows 不支持目录句柄 fsync；原子替换前已同步并关闭文件句柄。
func syncRootDir(_ *os.Root, _ string) error { return nil }
