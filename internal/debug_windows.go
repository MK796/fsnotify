package internal

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/windows"
)

func Debug(name string, mask uint32) {
	fmt.Fprintf(os.Stderr, "FSNOTIFY_DEBUG: %s  %-65s → %q\n",
		time.Now().Format("15:04:05.000000000"), windowsActionName(mask), filepath.ToSlash(name))
}

func windowsActionName(action uint32) string {
	switch action {
	case windows.FILE_ACTION_ADDED:
		return "FILE_ACTION_ADDED"
	case windows.FILE_ACTION_REMOVED:
		return "FILE_ACTION_REMOVED"
	case windows.FILE_ACTION_MODIFIED:
		return "FILE_ACTION_MODIFIED"
	case windows.FILE_ACTION_RENAMED_OLD_NAME:
		return "FILE_ACTION_RENAMED_OLD_NAME"
	case windows.FILE_ACTION_RENAMED_NEW_NAME:
		return "FILE_ACTION_RENAMED_NEW_NAME"
	default:
		return fmt.Sprintf("0x%x", action)
	}
}
