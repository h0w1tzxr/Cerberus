package master

import (
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

func verifyCandidateHash(mode HashMode, candidate, targetHash string) bool {
	targetHash = normalizeHash(targetHash)
	switch mode {
	case HashModeSHA256:
		hash := sha256.Sum256([]byte(candidate))
		return hex.EncodeToString(hash[:]) == targetHash
	case HashModeMD5:
		hash := md5.Sum([]byte(candidate))
		return hex.EncodeToString(hash[:]) == targetHash
	default:
		return false
	}
}

func sanitizeResultPassword(value string) string {
	return strings.TrimRight(value, "\r\n")
}
