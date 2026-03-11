//go:build windows

package main

import "fmt"

// diskUsage is a stub on Windows — storage is handled via wmic in collectStor.
func diskUsage(path string) (total, used, free int64, err error) {
	err = fmt.Errorf("statfs not available on windows")
	return
}
