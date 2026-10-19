//go:build windows

package main

// Windows: goat doesn't run there (sys_windows.go); no process figures.

func newProcSampleFunc() func() procTable {
	return func() procTable { return procTable{err: "not on Windows"} }
}

func memoryTotal() uint64 { return 0 }
