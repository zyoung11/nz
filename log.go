package main

import "fmt"

const (
	colorBlue   = "\x1b[38;2;138;173;244m"
	colorGreen  = "\x1b[38;2;166;218;149m"
	colorYellow = "\x1b[38;2;238;212;159m"
	colorRed    = "\x1b[38;2;237;135;150m"
	colorReset  = "\x1b[0m"
)

func logInfo(msg string, args ...any) {
	fmt.Printf(colorBlue+"[i] "+msg+colorReset+"\n", args...)
}

func logSuccess(msg string, args ...any) {
	fmt.Printf(colorGreen+"[+] "+msg+colorReset+"\n", args...)
}

func logWarn(msg string, args ...any) {
	fmt.Printf(colorYellow+"[!] "+msg+colorReset+"\n", args...)
}

func logError(msg string, args ...any) {
	fmt.Printf(colorRed+"[-] "+msg+colorReset+"\n", args...)
}
