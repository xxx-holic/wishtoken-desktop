package fenjue

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

// Bound to the pre-branding request descriptor, including the stable identity salt.
func TestWireDescriptorMatchesVerifiedBaseline(t *testing.T) {
	values := []string{API, Gateway, BYOKURL, wireValue("WC1LSUxPQ09ERS1GRUFUVVJF"), wireValue("WC1LSUxPQ09ERS1UQVNLSUQ="), wireValue("WC1LSUxPQ09ERS1NQUNISU5FSUQ="), wireValue("d2lzaHRva2VuLWtpbG8tbWFjaGluZQo=")}
	sum := sha256.Sum256([]byte(strings.Join(values, "\n")))
	if hex.EncodeToString(sum[:]) != "fe31f7ff2233f1066f6e4ec9e1ee2014be6d8a32ff88b26907ceef8aa98c59f9" {
		t.Fatal("verified provider wire descriptor changed")
	}
}
