//go:build !windows

package main

func detectWindowsService() bool {
	return false
}
