//go:build !linux

package main

import "os"

func filesystemKey(*os.File) string { return "" }
