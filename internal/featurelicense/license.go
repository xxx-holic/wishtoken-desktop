// Package featurelicense verifies issuer-signed, installation-bound feature codes.
package featurelicense

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	_ "embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

//go:embed verify-key.txt
var encodedPublicKey string

const LockedMessage = "焚诀通道已锁定，请在设置中输入此安装对应的有效授权码。"

type Claims struct {
	Version      int    `json:"v"`
	Product      string `json:"product"`
	Feature      string `json:"feature"`
	Installation string `json:"installation"`
	ID           string `json:"id"`
	IssuedAt     int64  `json:"issued_at"`
	ExpiresAt    int64  `json:"expires_at"`
}

type Status struct {
	Unlocked     bool   `json:"unlocked"`
	Installation string `json:"installation"`
	Reason       string `json:"reason"`
	ExpiresAt    int64  `json:"expires_at,omitempty"`
	LicenseID    string `json:"license_id,omitempty"`
}

type Manager struct {
	mu   sync.Mutex
	home string
	key  ed25519.PublicKey
	now  func() time.Time
}

func New(home string) *Manager {
	key, _ := base64.StdEncoding.DecodeString(strings.TrimSpace(encodedPublicKey))
	return NewWithKey(home, key)
}

// NewWithKey supports independently owned issuers and synthetic test fixtures.
// The application always calls New; no HTTP/config/env input can replace its key.
func NewWithKey(home string, key ed25519.PublicKey) *Manager {
	return &Manager{home: filepath.Join(home, "feature-license"), key: append(ed25519.PublicKey(nil), key...), now: time.Now}
}

func (m *Manager) installation() (string, error) {
	if err := os.MkdirAll(m.home, 0700); err != nil {
		return "", err
	}
	file := filepath.Join(m.home, "installation-id")
	raw, err := os.ReadFile(file)
	if os.IsNotExist(err) {
		b := make([]byte, 16)
		if _, err = rand.Read(b); err != nil {
			return "", err
		}
		id := "WT-" + strings.ToUpper(hex.EncodeToString(b))
		var f *os.File
		f, err = os.OpenFile(file, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err == nil {
			_, err = f.WriteString(id)
			closeErr := f.Close()
			if err != nil {
				return "", err
			}
			if closeErr != nil {
				return "", closeErr
			}
			return id, nil
		}
		if !os.IsExist(err) {
			return "", err
		}
		raw, err = os.ReadFile(file)
	}
	if err != nil {
		return "", err
	}
	id := strings.TrimSpace(string(raw))
	if len(id) != 35 || !strings.HasPrefix(id, "WT-") {
		return "", errors.New("invalid installation")
	}
	if _, err = hex.DecodeString(id[3:]); err != nil {
		return "", err
	}
	return id, nil
}

func (m *Manager) verify(code, installation string) (Claims, string) {
	var c Claims
	if len(m.key) != ed25519.PublicKeySize {
		return c, "issuer_unavailable"
	}
	parts := strings.Split(strings.TrimSpace(code), ".")
	if len(code) > 8192 || len(parts) != 3 || parts[0] != "WTK1" {
		return c, "invalid_code"
	}
	raw, e1 := base64.RawURLEncoding.DecodeString(parts[1])
	sig, e2 := base64.RawURLEncoding.DecodeString(parts[2])
	if e1 != nil || e2 != nil || !ed25519.Verify(m.key, []byte(parts[0]+"."+parts[1]), sig) {
		return c, "invalid_signature"
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&c) != nil || c.Version != 1 || c.Product != "wishtoken-desktop" || c.Feature != "fenjue" || c.ID == "" || len(c.ID) > 100 || c.IssuedAt <= 0 || c.ExpiresAt <= c.IssuedAt {
		return c, "invalid_claims"
	}
	if c.Installation != installation {
		return c, "wrong_installation"
	}
	now := m.now().Unix()
	if c.IssuedAt > now+300 {
		return c, "not_yet_valid"
	}
	if c.ExpiresAt <= now {
		return c, "expired"
	}
	return c, ""
}

func (m *Manager) status() Status {
	id, err := m.installation()
	if err != nil {
		return Status{Reason: "storage_error"}
	}
	s := Status{Installation: id, Reason: "not_activated"}
	raw, err := os.ReadFile(filepath.Join(m.home, "fenjue.license"))
	if os.IsNotExist(err) {
		return s
	}
	if err != nil {
		s.Reason = "storage_error"
		return s
	}
	c, reason := m.verify(string(raw), id)
	s.Reason = reason
	if reason == "" {
		s.Unlocked = true
		s.Reason = "active"
		s.ExpiresAt = c.ExpiresAt
		s.LicenseID = c.ID
	}
	return s
}

func (m *Manager) Status() Status { m.mu.Lock(); defer m.mu.Unlock(); return m.status() }
func (m *Manager) Require() error {
	if m == nil || !m.Status().Unlocked {
		return errors.New(LockedMessage)
	}
	return nil
}

func (m *Manager) Activate(code string) (Status, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	id, err := m.installation()
	if err != nil {
		return Status{Reason: "storage_error"}, errors.New("无法保存安装标识")
	}
	_, reason := m.verify(code, id)
	if reason != "" {
		messages := map[string]string{"wrong_installation": "授权码不属于此安装，请重新复制安装标识向签发方获取授权码。", "expired": "授权码已到期，请获取新的授权码。", "not_yet_valid": "系统时间早于授权签发时间，请检查系统时钟。", "issuer_unavailable": "客户端验签配置不可用，请更新客户端。"}
		message := messages[reason]
		if message == "" {
			message = "授权码校验失败，请完整粘贴签发方提供的有效授权码。"
		}
		return Status{Installation: id, Reason: reason}, errors.New(message)
	}
	file := filepath.Join(m.home, "fenjue.license")
	if err = os.WriteFile(file+".tmp", []byte(strings.TrimSpace(code)), 0600); err == nil {
		err = os.Rename(file+".tmp", file)
	}
	if err != nil {
		return Status{Installation: id, Reason: "storage_error"}, errors.New("授权码有效，但无法保存")
	}
	return m.status(), nil
}

func (m *Manager) Deactivate() (Status, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	err := os.Remove(filepath.Join(m.home, "fenjue.license"))
	if err != nil && !os.IsNotExist(err) {
		return Status{Reason: "storage_error"}, err
	}
	return m.status(), nil
}
