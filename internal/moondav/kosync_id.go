package moondav

import (
	"crypto/md5"
	"fmt"
	"io"
	"os"
	"strings"
)

func koreaderPartialMD5(filename string) (string, error) {
	f, err := os.Open(filename)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := md5.New() // KOReader uses MD5 here as a document identifier, not for security.
	const step int64 = 1024
	const sampleSize int64 = 1024

	for i := -1; i <= 10; i++ {
		maskedShift := uint((2 * i) & 0x1f)
		position := (uint64(step) << maskedShift) & 0xffffffff
		if _, err := f.Seek(int64(position), io.SeekStart); err != nil {
			return "", err
		}
		n, err := io.CopyN(h, f, sampleSize)
		if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
			return "", err
		}
		if n == 0 {
			break
		}
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

func (a *App) resolveBackendID(bookKey string) (string, bool) {
	mapping, ok := a.bookMap.ResolveMapping(bookKey)
	if !ok {
		return "", false
	}

	switch strings.ToLower(a.cfg.BackendType) {
	case "calibre-web-automated", "cwa":
		if mapping.EPUBPath != "" && a.cfg.LibraryRoot != "" {
			filename, err := a.safeLibraryPath(mapping.EPUBPath)
			if err == nil {
				if checksum, err := koreaderPartialMD5(filename); err == nil && checksum != "" {
					return checksum, true
				}
			}
		}
	}

	return mapping.BackendID, mapping.BackendID != ""
}
