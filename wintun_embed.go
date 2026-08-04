//go:build windows

package main

import (
	_ "embed"
)

//go:embed wintun.dll
var wintunDLL []byte
