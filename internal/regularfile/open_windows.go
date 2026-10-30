package regularfile

import "os"

// Native CreateFile returns ERROR_PIPE_BUSY rather than waiting for a pipe peer.
// The common retained-handle Stat check rejects pipes and devices before reading.
const openFlags = 0

func blocking(_ *os.File) error { return nil }
