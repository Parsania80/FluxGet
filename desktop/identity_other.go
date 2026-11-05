//go:build !linux

package main

import "errors"

func replacementFor(string) (*replacement, error) {
	return nil, errors.New("replacement is only available on Linux")
}
func replacementMatches(*replacement) bool { return false }
