//go:build linux

package main

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
	job, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open job lock: %w", err)
	}
	fail := func(err error) (*jobLock, error) {
		job.Close()
		return nil, err
	}
	jobInfo, err := job.Stat()
	if err != nil || !jobInfo.Mode().IsRegular() {
		return fail(errors.New("job lock must be a regular file"))
	}
	partialInfo, err := partial.Stat()
	if err != nil || !partialInfo.Mode().IsRegular() || os.SameFile(jobInfo, partialInfo) {
		return fail(errors.New("invalid partial file for job lock"))
	}
	if err := syscall.Flock(int(job.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return fail(fmt.Errorf("job is already owned or cannot be locked: %w", err))
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
