package otoha

import (
	"bytes"
	"compress/gzip"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

type Object map[string]any

func S(o Object, k string) string { s, _ := o[k].(string); return s }
func O(o Object, k string) Object {
	if v, ok := o[k].(map[string]any); ok {
		return Object(v)
	}
	if v, ok := o[k].(Object); ok {
		return v
	}
	return Object{}
}
func N(o Object, k string) int {
	if n, ok := o[k].(float64); ok {
		return int(n)
	}
	if n, ok := o[k].(int); ok {
		return n
	}
	return 0
}
func B(o Object, k string) bool {
	if b, ok := o[k].(bool); ok {
		return b
	}
	return N(o, k) != 0
}
func Items(o Object, k string) []Object {
	var r []Object
	for _, v := range Array(o[k]) {
		if m, ok := v.(map[string]any); ok {
			r = append(r, Object(m))
		}
	}
	return r
}
func Array(v any) []any { a, _ := v.([]any); return a }
func Home() string {
	if p := os.Getenv("OTOHA_HOME"); p != "" {
		return p
	}
	h, _ := os.UserHomeDir()
	return filepath.Join(h, ".config", "otoha")
}
func PrivateDir(p string) error {
	if err := os.MkdirAll(p, 0700); err != nil {
		return err
	}
	if runtime.GOOS == "windows" {
		who := os.Getenv("USERDOMAIN") + `\` + os.Getenv("USERNAME")
		if os.Getenv("USERDOMAIN") == "" {
			who = os.Getenv("USERNAME")
		}
		return exec.Command("icacls", p, "/inheritance:r", "/grant:r", who+":(OI)(CI)F").Run()
	}
	return os.Chmod(p, 0700)
}
func Save(p string, v any) error {
	if err := PrivateDir(filepath.Dir(p)); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(p), ".otoha-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(append(raw, '\n'))
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(tmp, p)
}
func Load(p string) (Object, error) {
	st, err := os.Stat(p)
	if err != nil {
		return nil, err
	}
	if runtime.GOOS != "windows" && st.Mode().Perm()&0077 != 0 {
		return nil, fmt.Errorf("private file permissions required: %s", p)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	var o Object
	err = json.Unmarshal(b, &o)
	return o, err
}
func ID() string {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return "a" + base64.RawURLEncoding.EncodeToString(b)
}

// Pack retains personal credentials and endpoints, never executable client files.
func Pack(o Object) (string, error) {
	b, err := json.Marshal(o)
	if err != nil {
		return "", err
	}
	var out bytes.Buffer
	z := gzip.NewWriter(&out)
	if _, err = z.Write(b); err != nil {
		return "", err
	}
	if err = z.Close(); err != nil {
		return "", err
	}
	return "otoha1_" + base64.RawURLEncoding.EncodeToString(out.Bytes()), nil
}
func Unpack(key string) (Object, error) {
	if !strings.HasPrefix(key, "otoha1_") || len(key) > 32768 {
		return nil, fmt.Errorf("invalid invitation key")
	}
	b, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(key, "otoha1_"))
	if err != nil {
		return nil, fmt.Errorf("invalid invitation encoding")
	}
	z, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	defer z.Close()
	b, err = io.ReadAll(io.LimitReader(z, 65537))
	if err != nil {
		return nil, err
	}
	if len(b) > 65536 {
		return nil, fmt.Errorf("invitation too large")
	}
	var o Object
	err = json.Unmarshal(b, &o)
	if err != nil {
		return nil, err
	}
	if S(o, "actor") == "" || S(o, "url") == "" {
		return nil, fmt.Errorf("invitation missing identity or endpoint")
	}
	if _, exists := o["client_files"]; exists {
		return nil, fmt.Errorf("executable bundles are not accepted as compact invitations")
	}
	return o, nil
}
func Clone(o Object) Object { b, _ := json.Marshal(o); var n Object; json.Unmarshal(b, &n); return n }
