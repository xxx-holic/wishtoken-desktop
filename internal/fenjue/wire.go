package fenjue

import "encoding/base64"

// Encoding keeps provider labels out of product source text. It is not
// encryption: the authenticated network peer and authorization page remain visible.
func wireValue(encoded string) string {
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		panic("invalid built-in provider descriptor")
	}
	return string(raw)
}
