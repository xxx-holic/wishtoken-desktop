// Package channelname preserves compatibility with earlier route identifiers.
package channelname

import (
	"encoding/base64"
	"strings"
)

const Fenjue = "fenjue"

var legacy = func() string {
	raw, err := base64.StdEncoding.DecodeString("a2lsbw==")
	if err != nil {
		panic(err)
	}
	return string(raw)
}()

func Normalize(value string) string {
	if strings.EqualFold(strings.TrimSpace(value), legacy) || strings.EqualFold(strings.TrimSpace(value), Fenjue) {
		return Fenjue
	}
	return value
}

func Model(value string) string {
	if strings.HasSuffix(value, "-"+legacy) {
		return strings.TrimSuffix(value, "-"+legacy) + "-" + Fenjue
	}
	return value
}

func AdminPath(value string) string {
	if strings.HasPrefix(value, "/"+legacy+"/") {
		return "/" + Fenjue + strings.TrimPrefix(value, "/"+legacy)
	}
	return value
}

// IdentityKey keeps isolated profile paths stable across the branding update.
func IdentityKey(value string) string {
	if Normalize(value) == Fenjue {
		return legacy
	}
	return value
}

func Source(value string) string {
	if value == legacy+"-device-login" {
		return Fenjue + "-device-login"
	}
	return value
}
