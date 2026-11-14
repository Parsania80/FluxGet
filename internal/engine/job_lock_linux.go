//go:build linux

package engine

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

type jobLock struct {
	job     *os.File
	partial *os.File
}

// lockJob keeps both lock files open until Close; callers must not unlink them.
func lockJob(path string, partial *os.File) (*jobLock, error) {
	if partial == nil {
		return nil, errors.New("partial file is required")
	}
	job, err := lockJobRecord(path)
	if err != nil {
		return nil, err
	}
	fail := func(err error) (*jobLock, error) { job.Close(); return nil, err }
	jobInfo, err := job.Stat()
	if err != nil {
		return fail(err)
	}
	partialInfo, err := partial.Stat()
	if err != nil || !partialInfo.Mode().IsRegular() || os.SameFile(jobInfo, partialInfo) {
		return fail(errors.New("invalid partial file for job lock"))
	}
	heldPartial, err := os.OpenFile(partial.Name(), os.O_RDWR|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return fail(fmt.Errorf("open partial lock: %w", err))
	}
	openedInfo, err := heldPartial.Stat()
	if err != nil || !os.SameFile(partialInfo, openedInfo) {
		heldPartial.Close()
		return fail(errors.New("partial file changed while acquiring lock"))
	}
	if err := syscall.Flock(int(heldPartial.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		heldPartial.Close()
		return fail(fmt.Errorf("partial file is already owned or cannot be locked: %w", err))
	}
	return &jobLock{job: job, partial: heldPartial}, nil
}

func (l *jobLock) Close() error {
	return errors.Join(l.partial.Close(), l.job.Close())
}

func jobActive(path string) (bool, error) {
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return false, fmt.Errorf("open job lock: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return false, errors.New("job lock must be a regular file")
	}
	err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspect job lock: %w", err)
	}
	return false, nil
}

func lockJobRecord(path string) (*os.File, error) {
	job, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, fmt.Errorf("open job lock: %w", err)
	}
	info, err := job.Stat()
	if err != nil || !info.Mode().IsRegular() {
		job.Close()
		return nil, errors.New("job lock must be a regular file")
	}
	if err := syscall.Flock(int(job.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		job.Close()
		return nil, fmt.Errorf("job is already owned or cannot be locked: %w", err)
	}
	return job, nil
}
