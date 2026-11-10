package mock

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"strconv"
)

// source derives every identifier and timing jitter from (seed, scenario, label).
// It uses SHA-256 rather than math/rand so output cannot change with Go releases.
type source struct {
	seed     uint64
	scenario string
}

func (s source) sum(label string) [32]byte {
	return sha256.Sum256([]byte("switchboard-mock\x00" + strconv.FormatUint(s.seed, 10) + "\x00" + s.scenario + "\x00" + label))
}

// uuid returns a version 4 shaped UUID string. The bits are deterministic, not random.
func (s source) uuid(label string) string {
	b := s.sum(label)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	h := hex.EncodeToString(b[:16])
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}

// jitter returns a deterministic value in [0, limit).
func (s source) jitter(label string, limit int64) int64 {
	if limit <= 0 {
		return 0
	}
	b := s.sum("jitter/" + label)
	return int64(binary.BigEndian.Uint64(b[:8]) % uint64(limit))
}
