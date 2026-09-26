package cli

import (
	"os"

	"golang.org/x/sys/unix"
)

func discardTypeahead(f *os.File) {
	const fread = 1 // FREAD: flush the input queue only
	_ = unix.IoctlSetPointerInt(int(f.Fd()), unix.TIOCFLUSH, fread)
}
