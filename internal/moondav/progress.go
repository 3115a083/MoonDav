package moondav

import (
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

var percentRE = regexp.MustCompile(`(?i)([0-9]+(?:\.[0-9]+)?)%\s*$`)

type MoonPosition struct {
	Raw     []byte
	Percent float64
	Valid   bool
}

func ParseMoonPosition(b []byte) MoonPosition {
	s := strings.TrimSpace(string(b))
	m := percentRE.FindStringSubmatch(s)
	if len(m) != 2 {
		return MoonPosition{Raw: append([]byte(nil), b...)}
	}
	p, err := strconv.ParseFloat(m[1], 64)
	if err != nil || p < 0 || p > 100 {
		return MoonPosition{Raw: append([]byte(nil), b...)}
	}
	return MoonPosition{Raw: append([]byte(nil), b...), Percent: p, Valid: true}
}

func IsPositionPath(p string) bool {
	p = filepath.ToSlash(p)
	return strings.HasSuffix(strings.ToLower(p), ".po") && strings.Contains(p, "/.Moon+/Cache/")
}

func BookKeyFromPOPath(p string) string {
	base := filepath.Base(p)
	return strings.TrimSuffix(strings.ToLower(base), ".po")
}
