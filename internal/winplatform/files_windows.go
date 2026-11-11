//go:build windows

// Package winplatform contains the Windows file and process primitives shared
// by the desktop, queue and browser bridge.
package winplatform

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

// OpenFile refuses leaf reparse points and permits deletion/renaming while an
// already verified handle is open, as required by hard-link publication.
func OpenFile(path string, flag int, mode os.FileMode) (*os.File, error) {
	if err := validatePath(path); err != nil {
		return nil, err
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if !strings.HasPrefix(absolute, `\\?\`) {
		if strings.HasPrefix(absolute, `\\`) {
			absolute = `\\?\UNC\` + strings.TrimPrefix(absolute, `\\`)
		} else {
			absolute = `\\?\` + absolute
		}
	}
	name, err := windows.UTF16PtrFromString(absolute)
	if err != nil {
		return nil, err
	}
	access := uint32(windows.GENERIC_READ)
	if flag&(os.O_RDWR|os.O_WRONLY) != 0 {
		access |= windows.GENERIC_WRITE
	}
	disposition := uint32(windows.OPEN_EXISTING)
	if flag&os.O_CREATE != 0 {
		disposition = windows.OPEN_ALWAYS
		if flag&os.O_EXCL != 0 {
			disposition = windows.CREATE_NEW
		}
	}
	h, err := windows.CreateFile(name, access, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, disposition, windows.FILE_FLAG_OPEN_REPARSE_POINT|windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	f := os.NewFile(uintptr(h), path)
	var info windows.ByHandleFileInformation
	if err = windows.GetFileInformationByHandle(h, &info); err != nil || info.FileAttributes&(windows.FILE_ATTRIBUTE_REPARSE_POINT|windows.FILE_ATTRIBUTE_DIRECTORY) != 0 {
		f.Close()
		if err == nil {
			err = errors.New("expected a regular file without a reparse point")
		}
		return nil, err
	}
	if flag&os.O_TRUNC != 0 {
		if err = f.Truncate(0); err != nil {
			f.Close()
			return nil, err
		}
	}
	return f, nil
}

// Lock a byte beyond valid file data: Windows locks are mandatory, so locking
// ordinary data would also block the transfer's other handles.
func LockFile(file *os.File) error {
	o := windows.Overlapped{Offset: 0xffffffff, OffsetHigh: 0x7fffffff}
	return windows.LockFileEx(windows.Handle(file.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &o)
}
func Busy(err error) bool {
	return errors.Is(err, windows.ERROR_LOCK_VIOLATION) || errors.Is(err, windows.ERROR_SHARING_VIOLATION)
}
func OpenLocked(path string, create bool) (*os.File, error) {
	flag := os.O_RDWR
	if create {
		flag |= os.O_CREATE
	}
	f, err := OpenFile(path, flag, 0600)
	if err != nil {
		return nil, err
	}
	if err = LockFile(f); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}
func Identity(file *os.File) (uint64, uint64, uint64, error) {
	var info windows.ByHandleFileInformation
	err := windows.GetFileInformationByHandle(windows.Handle(file.Fd()), &info)
	if err != nil {
		return 0, 0, 0, err
	}
	if info.FileAttributes&(windows.FILE_ATTRIBUTE_DIRECTORY|windows.FILE_ATTRIBUTE_REPARSE_POINT) != 0 {
		return 0, 0, 0, errors.New("file identity is not regular")
	}
	return uint64(info.VolumeSerialNumber), uint64(info.FileIndexHigh)<<32 | uint64(info.FileIndexLow), uint64(info.NumberOfLinks), nil
}
func UserSID() (string, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return "", err
	}
	return user.User.Sid.String(), nil
}
func ProcessSID(pid uint32) (string, error) {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return "", err
	}
	defer windows.CloseHandle(h)
	var token windows.Token
	if err = windows.OpenProcessToken(h, windows.TOKEN_QUERY, &token); err != nil {
		return "", err
	}
	defer token.Close()
	user, err := token.GetTokenUser()
	if err != nil {
		return "", err
	}
	return user.User.Sid.String(), nil
}

func validatePath(path string) error {
	rest := strings.TrimPrefix(filepath.Clean(path), filepath.VolumeName(path))
	for _, component := range strings.FieldsFunc(rest, func(r rune) bool { return r == '\\' || r == '/' }) {
		base := strings.ToUpper(strings.SplitN(component, ".", 2)[0])
		invalid := strings.ContainsAny(component, `<>:"|?*`) || strings.HasSuffix(component, ".") || strings.HasSuffix(component, " ")
		switch base {
		case "CON", "PRN", "AUX", "NUL", "CONIN$", "CONOUT$":
			invalid = true
		}
		suffix := strings.TrimPrefix(strings.TrimPrefix(base, "COM"), "LPT")
		if (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) && len([]rune(suffix)) == 1 && strings.Contains("123456789¹²³", suffix) {
			invalid = true
		}
		if invalid {
			return errors.New("unsupported Windows filename")
		}
	}
	return nil
}
