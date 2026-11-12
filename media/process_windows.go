//go:build windows

package media

import (
	"errors"
	"os/exec"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

type processTree struct{ job windows.Handle }

// Suspend before any tool code runs, attach a kill-on-close Job Object, then
// resume the only thread. Assigning after an ordinary Start races child creation.
func startTree(cmd *exec.Cmd) (*processTree, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, err
	}
	tree := &processTree{job: job}
	ok := false
	defer func() {
		if !ok {
			tree.close()
		}
	}()
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits))); err != nil {
		return nil, err
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW | windows.CREATE_SUSPENDED}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	failed := func(err error) (*processTree, error) {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return nil, err
	}
	process, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(cmd.Process.Pid))
	if err != nil {
		return failed(err)
	}
	err = windows.AssignProcessToJobObject(job, process)
	windows.CloseHandle(process)
	if err != nil {
		return failed(err)
	}
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPTHREAD, 0)
	if err != nil {
		return failed(err)
	}
	defer windows.CloseHandle(snapshot)
	entry := windows.ThreadEntry32{Size: uint32(unsafe.Sizeof(windows.ThreadEntry32{}))}
	for err = windows.Thread32First(snapshot, &entry); err == nil; err = windows.Thread32Next(snapshot, &entry) {
		if entry.OwnerProcessID != uint32(cmd.Process.Pid) {
			continue
		}
		thread, openErr := windows.OpenThread(windows.THREAD_SUSPEND_RESUME, false, entry.ThreadID)
		if openErr != nil {
			return failed(openErr)
		}
		previous, resumeErr := windows.ResumeThread(thread)
		windows.CloseHandle(thread)
		if resumeErr != nil {
			return failed(resumeErr)
		}
		// Enumeration order is unspecified. Ignore already-running auxiliary
		// threads; our primary thread has exactly one creation suspension.
		if previous == 0 {
			continue
		}
		if previous != 1 {
			return failed(errors.New("unexpected media thread suspension count"))
		}
		ok = true
		return tree, nil
	}
	return failed(errors.New("suspended media thread not found"))
}

// Hidden console tools cannot receive console control events reliably. The
// spike uses whole-job termination on Windows; graceful recovery is a later gate.
func (t *processTree) interrupt() { t.kill() }
func (t *processTree) kill()      { _ = windows.TerminateJobObject(t.job, 1) }
func (t *processTree) close()     { t.kill(); _ = windows.CloseHandle(t.job) }
