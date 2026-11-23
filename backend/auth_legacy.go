package main

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/binary"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Aptabase used the ASP.NET Core v5 cookie ticket with the default v0
// Data Protection envelope (AES-256-CBC, HMAC-SHA256). Legacy keys are read only.
type authLegacyKey struct {
	XMLName    xml.Name  `xml:"key"`
	ID         string    `xml:"id,attr"`
	Version    int       `xml:"version,attr"`
	CreatedAt  time.Time `xml:"creationDate"`
	Descriptor struct {
		Encryption authLegacyAlgorithm `xml:"encryption"`
		Validation authLegacyAlgorithm `xml:"validation"`
		MasterKey  string              `xml:"masterKey>value"`
	} `xml:"descriptor>descriptor"`
}

type authLegacyAlgorithm struct {
	Name string `xml:"algorithm,attr"`
}

const authLegacyMagic = "\x09\xf0\xc9\xf0"

func readLegacyAuthSession(ctx context.Context, value, region string, managed *authLegacySSMCache) (*authSessionTicket, error) {
	if len(value) > authMaxCookieSize || !strings.HasPrefix(value, "CfDJ8") {
		return nil, nil
	}
	payload, err := base64.RawURLEncoding.Strict().DecodeString(value)
	const minPayload = 20 + 3*aes.BlockSize + sha256.Size
	if err != nil || len(payload) < minPayload || (len(payload)-minPayload)%aes.BlockSize != 0 || string(payload[:4]) != authLegacyMagic {
		return nil, nil
	}
	keyID := authLegacyGUID(payload[4:20])
	dir := os.Getenv("ASPNET_DATA_PROTECTION_KEY_DIR")
	key, err := authLegacyFileKey(dir, keyID)
	if err != nil {
		return nil, err
	}
	if key == nil && (region == "EU" || region == "US") {
		ring, err := managed.get(ctx)
		if err != nil {
			return nil, err
		}
		key = ring.keys[keyID]
		if key != nil && ring.revoked(*key) {
			return nil, nil
		}
	}
	if key == nil || key.Version != 1 || !strings.EqualFold(keyID, key.ID) || key.Descriptor.Encryption.Name != "AES_256_CBC" || key.Descriptor.Validation.Name != "HMACSHA256" {
		return nil, nil
	}
	master, err := authDecodeBase64(key.Descriptor.MasterKey)
	if err != nil || len(master) != 64 {
		return nil, nil
	}
	appName, ok := os.LookupEnv("ASPNET_DATA_PROTECTION_APP_NAME")
	if !ok {
		appName = "/app/"
	}
	purposes := []string{"Microsoft.AspNetCore.Authentication.Cookies.CookieAuthenticationMiddleware", "Cookies", "v2"}
	if appName != "" {
		purposes = append([]string{appName}, purposes...)
	}
	plaintext := authLegacyUnprotect(master, payload, purposes)
	ticket := authLegacyTicket(plaintext)
	if ticket == nil {
		return nil, nil
	}
	revoked, err := authLegacyFileRevoked(dir, *key)
	if revoked || err != nil {
		return nil, err
	}
	return ticket, nil
}

func authLegacyFileKey(dir, keyID string) (*authLegacyKey, error) {
	if dir == "" {
		return nil, nil
	}
	var key authLegacyKey
	if err := authLegacyDecodeXML(filepath.Join(dir, "key-"+keyID+".xml"), &key); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read legacy Data Protection key: %w", err)
	}
	return &key, nil
}

func authLegacyGUID(b []byte) string {
	return fmt.Sprintf("%08x-%04x-%04x-%x-%x", binary.LittleEndian.Uint32(b[:4]),
		binary.LittleEndian.Uint16(b[4:6]), binary.LittleEndian.Uint16(b[6:8]), b[8:10], b[10:16])
}

func authLegacyDecodeXML(path string, dst any) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	return xml.NewDecoder(io.LimitReader(file, 64<<10)).Decode(dst)
}

type authLegacyRevocation struct {
	XMLName xml.Name  `xml:"revocation"`
	Date    time.Time `xml:"revocationDate"`
	Key     struct {
		ID string `xml:"id,attr"`
	} `xml:"key"`
}

func (r authLegacyRevocation) valid() bool {
	return r.Key.ID != "" && (r.Key.ID != "*" || !r.Date.IsZero())
}

func (r authLegacyRevocation) matches(key authLegacyKey) bool {
	return strings.EqualFold(r.Key.ID, key.ID) || (r.Key.ID == "*" && key.CreatedAt.Before(r.Date))
}

func authLegacyFileRevoked(dir string, key authLegacyKey) (bool, error) {
	if dir == "" {
		return false, nil
	}
	files, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	for _, file := range files {
		if file.IsDir() || !strings.HasPrefix(file.Name(), "revocation-") || !strings.HasSuffix(file.Name(), ".xml") {
			continue
		}
		var revoke authLegacyRevocation
		if err := authLegacyDecodeXML(filepath.Join(dir, file.Name()), &revoke); err != nil {
			return false, fmt.Errorf("read legacy Data Protection revocation: %w", err)
		}
		if !revoke.valid() {
			return false, errors.New("invalid legacy Data Protection revocation")
		}
		if revoke.matches(key) {
			return true, nil
		}
	}
	return false, nil
}

func authLegacyKDF(master, label, contextHeader, modifier []byte) []byte {
	// SP800-108 counter mode needs exactly one HMAC-SHA512 block for the two 32-byte keys.
	mac := hmac.New(sha512.New, master)
	mac.Write([]byte{0, 0, 0, 1})
	mac.Write(label)
	mac.Write([]byte{0})
	mac.Write(contextHeader)
	mac.Write(modifier)
	mac.Write([]byte{0, 0, 2, 0}) // 512 bits of key material
	return mac.Sum(nil)
}

var authLegacyContextHeader = func() []byte {
	header := []byte{0, 0}
	for _, size := range []uint32{32, 16, 32, 32} {
		header = binary.BigEndian.AppendUint32(header, size)
	}
	keys := authLegacyKDF(nil, nil, nil, nil)
	block, _ := aes.NewCipher(keys[:32])
	empty := bytes.Repeat([]byte{aes.BlockSize}, aes.BlockSize)
	block.Encrypt(empty, empty)
	return hmac.New(sha256.New, keys[32:]).Sum(append(header, empty...))
}()

func authLegacyUnprotect(master, payload []byte, purposes []string) []byte {
	label := binary.BigEndian.AppendUint32(bytes.Clone(payload[:20]), uint32(len(purposes)))
	for _, purpose := range purposes {
		label = binary.AppendUvarint(label, uint64(len(purpose)))
		label = append(label, purpose...)
	}
	modifier := payload[20:36]
	macOffset := len(payload) - sha256.Size
	iv, ciphertext := payload[36:52], payload[52:macOffset]
	if len(ciphertext) == 0 || len(ciphertext)%aes.BlockSize != 0 {
		return nil
	}
	keys := authLegacyKDF(master, label, authLegacyContextHeader, modifier)
	mac := hmac.New(sha256.New, keys[32:])
	mac.Write(payload[36:macOffset])
	if !hmac.Equal(mac.Sum(nil), payload[macOffset:]) {
		return nil
	}
	block, _ := aes.NewCipher(keys[:32])
	plaintext := make([]byte, len(ciphertext))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(plaintext, ciphertext)
	padding := plaintext[len(plaintext)-1]
	if padding == 0 || padding > aes.BlockSize {
		return nil
	}
	for _, b := range plaintext[len(plaintext)-int(padding):] {
		if b != padding {
			return nil
		}
	}
	return plaintext[:len(plaintext)-int(padding)]
}

func authLegacyTicket(plaintext []byte) *authSessionTicket {
	r := authLegacyReader{Reader: bytes.NewReader(plaintext)}
	if r.number() != 5 || r.text() != "Cookies" || r.number() != 1 || r.text() == "" {
		return nil
	}
	_ = r.text() // name claim type
	_ = r.text() // role claim type
	claims := make(map[string]string)
	for i := r.count(); i > 0; i-- {
		claim, value := r.text(), r.text()
		if _, ok := claims[claim]; !ok {
			claims[claim] = value
		}
		_, _, _ = r.text(), r.text(), r.text() // value type, issuer, original issuer
		for j := r.count(); j > 0; j-- {
			_, _ = r.text(), r.text()
		}
	}
	if r.flag() {
		_ = r.text() // bootstrap context
	}
	if r.flag() || r.number() != 1 { // Aptabase did not create actor identities
		return nil
	}
	props := make(map[string]string)
	for i := r.count(); i > 0; i-- {
		key, value := r.text(), r.text()
		props[key] = value
	}
	if r.err != nil || r.Len() != 0 || claims["id"] == "" {
		return nil
	}
	issued, iErr := http.ParseTime(props[".issued"])
	expires, eErr := http.ParseTime(props[".expires"])
	if iErr != nil || eErr != nil {
		return nil
	}
	_, persistent := props[".persistent"]
	return &authSessionTicket{
		Identity:    Identity{ID: claims["id"], Name: claims["name"], Email: claims["email"], Username: claims["username"], Role: claims["role"]},
		authSession: authSession{IssuedAt: issued.Unix(), ExpiresAt: expires.Unix(), Persistent: persistent, legacy: true},
	}
}

type authLegacyReader struct {
	*bytes.Reader
	err error
}

func (r *authLegacyReader) number() int32 {
	var n int32
	if r.err == nil {
		r.err = binary.Read(r.Reader, binary.LittleEndian, &n)
	}
	return n
}

func (r *authLegacyReader) count() int32 {
	n := r.number()
	if n < 0 || int(n) > r.Len() {
		r.err = errors.New("invalid ASP.NET ticket count")
		return 0
	}
	return n
}

func (r *authLegacyReader) text() string {
	if r.err != nil {
		return ""
	}
	n, err := binary.ReadUvarint(r.Reader)
	if err != nil || n > uint64(r.Len()) {
		r.err = errors.New("invalid ASP.NET ticket string")
		return ""
	}
	b := make([]byte, n)
	_, r.err = io.ReadFull(r.Reader, b)
	return string(b)
}

func (r *authLegacyReader) flag() bool {
	if r.err != nil {
		return false
	}
	b, err := r.ReadByte()
	r.err = err
	return b != 0
}
