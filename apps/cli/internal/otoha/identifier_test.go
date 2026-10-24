package otoha

import (
	"regexp"
	"testing"
)

func TestOperationIDsMatchServerProtocol(t *testing.T) {
	valid := regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,79}$`)
	for i := 0; i < 128; i++ {
		if id := ID(); !valid.MatchString(id) {
			t.Fatalf("invalid operation ID %q", id)
		}
	}
}
