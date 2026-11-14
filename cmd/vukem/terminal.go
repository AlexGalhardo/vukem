package main

import "os"

// isTerminal reports whether f is a terminal a person types at. A character
// device is not enough: the null device (/dev/null, NUL) is one too, and a
// script that redirects stdin there must not get prompts.
func isTerminal(f *os.File) bool {
	info, err := f.Stat()
	if err != nil || info.Mode()&os.ModeCharDevice == 0 {
		return false
	}

	null, err := os.Stat(os.DevNull)

	return err != nil || !os.SameFile(info, null)
}
