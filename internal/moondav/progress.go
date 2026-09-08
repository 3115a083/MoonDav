package moondav

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

var moonPositionRE = regexp.MustCompile(`^([0-9]+)\*([0-9]+)@([0-9]+)#([0-9]+):([0-9]+(?:\.[0-9]+)?)%$`)

type MoonPosition struct {
	Raw         []byte
	TimestampMS int64
	Chapter     int
	Section     int
	Offset      int64
	Percent     float64
	Valid       bool
}

func ParseMoonPosition(b []byte) MoonPosition {
	raw := append([]byte(nil), b...)
	s := strings.TrimSpace(string(b))
	m := moonPositionRE.FindStringSubmatch(s)
	if len(m) != 6 {
		return MoonPosition{Raw: raw}
	}
	ts, err1 := strconv.ParseInt(m[1], 10, 64)
	chapter, err2 := strconv.Atoi(m[2])
	section, err3 := strconv.Atoi(m[3])
	offset, err4 := strconv.ParseInt(m[4], 10, 64)
	percent, err5 := strconv.ParseFloat(m[5], 64)
	if err1 != nil || err2 != nil || err3 != nil || err4 != nil || err5 != nil ||
		ts <= 0 || chapter < 0 || section < 0 || offset < 0 || percent < 0 || percent > 100 {
		return MoonPosition{Raw: raw}
	}
	return MoonPosition{
		Raw: raw, TimestampMS: ts, Chapter: chapter, Section: section,
		Offset: offset, Percent: percent, Valid: true,
	}
}

func EncodeMoonPosition(p MoonPosition) ([]byte, error) {
	if p.TimestampMS <= 0 || p.Chapter < 0 || p.Section < 0 || p.Offset < 0 || p.Percent < 0 || p.Percent > 100 {
		return nil, fmt.Errorf("invalid Moon+ position")
	}
	return []byte(fmt.Sprintf("%d*%d@%d#%d:%.1f%%", p.TimestampMS, p.Chapter, p.Section, p.Offset, p.Percent)), nil
}

func IsPositionPath(p string) bool {
	p = filepath.ToSlash(p)
	return strings.HasSuffix(strings.ToLower(p), ".po") && strings.Contains(p, "/.Moon+/Cache/")
}

func BookKeyFromPOPath(p string) string {
	base := filepath.Base(p)
	return strings.TrimSuffix(strings.ToLower(base), ".po")
}
