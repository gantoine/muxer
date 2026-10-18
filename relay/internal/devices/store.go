package devices

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

const (
	pairingCodeAlphabet  = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"
	pairingCodeLength    = 8
	PairingCodeLifetime  = 10 * time.Minute
	maxFailedPairingRuns = 10
	maxDeviceNameBytes   = 80
)

var (
	ErrInvalidPairingCode = errors.New("invalid or expired pairing code")
	ErrUnknownDevice      = errors.New("unknown device")
)

type Device struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	TokenHash string    `json:"token_hash"`
	PairedAt  time.Time `json:"paired_at"`
	LastSeen  time.Time `json:"last_seen"`
}

type pendingCode struct {
	code      string
	expiresAt time.Time
}

type Store struct {
	path string
	now  func() time.Time

	mu             sync.Mutex
	devices        []Device
	pending        *pendingCode
	failedAttempts int
}

func Open(path string) (*Store, error) {
	store := &Store{path: path, now: time.Now}
	contents, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return store, nil
	case err != nil:
		return nil, err
	}
	if err := json.Unmarshal(contents, &store.devices); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *Store) NewPairingCode() (string, time.Time, error) {
	code, err := randomCode()
	if err != nil {
		return "", time.Time{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	expiresAt := s.now().Add(PairingCodeLifetime)
	s.pending = &pendingCode{code: code, expiresAt: expiresAt}
	s.failedAttempts = 0
	return code, expiresAt, nil
}

func (s *Store) Pair(code, deviceName string) (Device, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	normalized := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(code), "-", ""))
	if s.pending == nil || s.now().After(s.pending.expiresAt) {
		s.pending = nil
		return Device{}, "", ErrInvalidPairingCode
	}
	if subtle.ConstantTimeCompare([]byte(normalized), []byte(s.pending.code)) != 1 {
		s.failedAttempts++
		if s.failedAttempts >= maxFailedPairingRuns {
			s.pending = nil
		}
		return Device{}, "", ErrInvalidPairingCode
	}
	s.pending = nil

	token, err := randomToken()
	if err != nil {
		return Device{}, "", err
	}
	id, err := randomHex(8)
	if err != nil {
		return Device{}, "", err
	}
	name := strings.TrimSpace(deviceName)
	if name == "" {
		name = "Android device"
	}
	if len(name) > maxDeviceNameBytes {
		name = strings.ToValidUTF8(name[:maxDeviceNameBytes], "")
	}
	now := s.now()
	device := Device{ID: id, Name: name, TokenHash: hashToken(token), PairedAt: now, LastSeen: now}
	if err := s.saveLocked(append(slices.Clone(s.devices), device)); err != nil {
		return Device{}, "", err
	}
	return device, token, nil
}

func (s *Store) Authenticate(token string) (Device, bool) {
	if token == "" {
		return Device{}, false
	}
	hash := hashToken(token)
	s.mu.Lock()
	defer s.mu.Unlock()
	for index := range s.devices {
		if subtle.ConstantTimeCompare([]byte(s.devices[index].TokenHash), []byte(hash)) == 1 {
			seen := slices.Clone(s.devices)
			seen[index].LastSeen = s.now()
			if s.saveLocked(seen) != nil {
				s.devices[index].LastSeen = seen[index].LastSeen
			}
			return s.devices[index], true
		}
	}
	return Device{}, false
}

func (s *Store) List() []Device {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Device(nil), s.devices...)
}

func (s *Store) Revoke(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	index := slices.IndexFunc(s.devices, func(device Device) bool { return device.ID == id })
	if index < 0 {
		return ErrUnknownDevice
	}
	return s.saveLocked(slices.Delete(slices.Clone(s.devices), index, index+1))
}

func (s *Store) IsRevoked(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, device := range s.devices {
		if device.ID == id {
			return false
		}
	}
	return true
}

func (s *Store) saveLocked(next []Device) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	contents, err := json.MarshalIndent(next, "", "  ")
	if err != nil {
		return err
	}
	temporary := s.path + ".tmp"
	if err := os.WriteFile(temporary, contents, 0o600); err != nil {
		return err
	}
	if err := os.Rename(temporary, s.path); err != nil {
		return err
	}
	s.devices = next
	return nil
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func randomToken() (string, error) {
	buffer := make([]byte, 32)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buffer), nil
}

func randomHex(size int) (string, error) {
	buffer := make([]byte, size)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return hex.EncodeToString(buffer), nil
}

func randomCode() (string, error) {
	unbiasedLimit := byte(256 - 256%len(pairingCodeAlphabet))
	code := make([]byte, 0, pairingCodeLength)
	buffer := make([]byte, 1)
	for len(code) < pairingCodeLength {
		if _, err := rand.Read(buffer); err != nil {
			return "", err
		}
		if buffer[0] >= unbiasedLimit {
			continue
		}
		code = append(code, pairingCodeAlphabet[int(buffer[0])%len(pairingCodeAlphabet)])
	}
	return string(code), nil
}
