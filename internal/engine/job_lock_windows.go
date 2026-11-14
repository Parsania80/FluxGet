//go:build windows

package engine

import (
	"errors"
	"fmt"
	"github.com/Parsania80/FluxGet/internal/winplatform"
	"os"
)

type jobLock struct{ job, partial *os.File }

func lockJob(path string, partial *os.File) (*jobLock, error) {
	if partial == nil {
		return nil, errors.New("partial file is required")
	}
	job, err := lockJobRecord(path)
	if err != nil {
		return nil, err
	}
	held, err := winplatform.OpenFile(partial.Name(), os.O_RDWR, 0600)
	if err != nil {
		job.Close()
		return nil, err
	}
	a, e1 := partial.Stat()
	b, e2 := held.Stat()
	j, e3 := job.Stat()
	if e1 != nil || e2 != nil || e3 != nil || !os.SameFile(a, b) || os.SameFile(a, j) {
		held.Close()
		job.Close()
		return nil, errors.New("partial file changed while acquiring ownership")
	}
	if err = winplatform.LockFile(held); err != nil {
		held.Close()
		job.Close()
		return nil, fmt.Errorf("partial is already owned: %w", err)
	}
	return &jobLock{job: job, partial: held}, nil
}
func (l *jobLock) Close() error                   { return errors.Join(l.partial.Close(), l.job.Close()) }
func lockJobRecord(path string) (*os.File, error) { return winplatform.OpenLocked(path, true) }
func jobActive(path string) (bool, error) {
	f, err := winplatform.OpenLocked(path, false)
	if winplatform.Busy(err) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	f.Close()
	return false, nil
}
