//go:build darwin || linux

package main

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"
)

func authHealthDirSafe(dir string) bool {
	if !filepath.IsAbs(dir) || filepath.Clean(dir) != dir {
		return false
	}
	for path := dir; ; path = filepath.Dir(path) {
		info, err := os.Lstat(path)
		if err != nil || !info.IsDir() || info.Mode().Perm()&0022 != 0 {
			return false
		}
		if path == "/" {
			return true
		}
	}
}

func readHealthAuth(path string) ([]byte, error) {
	invalid := errors.New("authorization file unavailable")
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, invalid
	}
	f := os.NewFile(uintptr(fd), "auth")
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Size() <= 0 || info.Size() > 65536 {
		return nil, invalid
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Nlink != 1 || stat.Uid != uint32(os.Getuid()) {
		return nil, invalid
	}
	raw, err := io.ReadAll(io.LimitReader(f, 65537))
	if err != nil || int64(len(raw)) != info.Size() {
		return nil, invalid
	}
	after, err := f.Stat()
	if err != nil || after.Size() != info.Size() || after.ModTime() != info.ModTime() {
		return nil, invalid
	}
	return raw, nil
}
