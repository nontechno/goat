//go:build unix && !linux && !darwin && !freebsd

package pasture

// peerUID is unknown here; the 0700 socket directory is the only guard.
func peerUID(int) (int, error) { return -1, nil }

func processName(int) string { return "" }
func processDir(int) string  { return "" }
