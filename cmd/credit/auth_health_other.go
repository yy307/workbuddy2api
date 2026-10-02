//go:build !darwin && !linux

package main

import "errors"

func authHealthDirSafe(string) bool { return false }
func readHealthAuth(string) ([]byte, error) {
	return nil, errors.New("unsupported private-file platform")
}
