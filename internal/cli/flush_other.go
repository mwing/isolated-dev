//go:build !darwin && !linux

package cli

import "os"

func discardTypeahead(*os.File) {}
