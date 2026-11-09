package crashlog

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// lockPending is a no-op: the runtime's duplicate handle keeps the file open,
// and Windows refers other opens to that handle's share mode.
func lockPending(*os.File) error { return nil }

// pendingInUse reports whether another handle has path open, by asking for
// exclusive access: a running owner's handle makes that a sharing violation.
func pendingInUse(path string) bool {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return false
	}
	h, err := windows.CreateFile(name, windows.GENERIC_READ, 0, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return errors.Is(err, windows.ERROR_SHARING_VIOLATION)
	}
	_ = windows.CloseHandle(h)
	return false
}
