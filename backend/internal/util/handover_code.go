package util

import (
	"crypto/rand"
	"math/big"
)

// maxHandoverCode is the exclusive upper bound of the 6-digit code space.
var maxHandoverCode = big.NewInt(1000000)

// GenerateHandoverCode returns a cryptographically random 6-digit handover
// code, zero-padded (e.g. "042817"). It is one-time and short-lived.
func GenerateHandoverCode() (string, error) {
	n, err := rand.Int(rand.Reader, maxHandoverCode)
	if err != nil {
		return "", err
	}
	return FormatHandoverCode(n.Int64()), nil
}

// FormatHandoverCode zero-pads a code value to 6 digits.
func FormatHandoverCode(v int64) string {
	if v < 0 {
		v = -v
	}
	return leftPad6(v)
}

func leftPad6(v int64) string {
	digits := []byte("000000")
	for i := 5; i >= 0; i-- {
		digits[i] = byte('0' + v%10)
		v /= 10
	}
	return string(digits)
}
