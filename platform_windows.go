//go:build windows

package main

func isWindows() bool { return true }

func runSystrayWindows() {
	runSystray()
}
