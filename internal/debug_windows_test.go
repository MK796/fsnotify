package internal

import (
	"testing"

	"golang.org/x/sys/windows"
)

func TestWindowsActionName(t *testing.T) {
	tests := []struct {
		action uint32
		want   string
	}{
		{windows.FILE_ACTION_ADDED, "FILE_ACTION_ADDED"},
		{windows.FILE_ACTION_REMOVED, "FILE_ACTION_REMOVED"},
		{windows.FILE_ACTION_MODIFIED, "FILE_ACTION_MODIFIED"},
		{windows.FILE_ACTION_RENAMED_OLD_NAME, "FILE_ACTION_RENAMED_OLD_NAME"},
		{windows.FILE_ACTION_RENAMED_NEW_NAME, "FILE_ACTION_RENAMED_NEW_NAME"},
		{0, "0x0"},
		{99, "0x63"},
	}

	for _, tt := range tests {
		if got := windowsActionName(tt.action); got != tt.want {
			t.Errorf("windowsActionName(%d) = %q; want %q", tt.action, got, tt.want)
		}
	}
}
