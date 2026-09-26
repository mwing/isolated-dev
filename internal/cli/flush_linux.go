package cli

import (
	"os"

	"golang.org/x/sys/unix"
)

func discardTypeahead(f *os.File) {
	_ = unix.IoctlSetInt(int(f.Fd()), unix.TCFLSH, unix.TCIFLUSH)
}
