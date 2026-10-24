package otoha

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/binary"
	"encoding/pem"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// A first local name lookup can take >800ms even though that route is healthy.
// Use real Go DNS and SSH, with an isolated delayed DNS responder, rather than
// accepting a label or mocking the selected transport.
func TestPreferredRouteSurvivesSlowInitialNameLookup(t *testing.T) {
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := x509.MarshalPKCS8PrivateKey(private)
	keyFile := filepath.Join(t.TempDir(), "key")
	if err = os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: encoded}), 0600); err != nil {
		t.Fatal(err)
	}
	signer, _ := ssh.NewSignerFromKey(private)
	serverConfig := &ssh.ServerConfig{PublicKeyCallback: func(ssh.ConnMetadata, ssh.PublicKey) (*ssh.Permissions, error) { return nil, nil }}
	serverConfig.AddHostKey(signer)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				server, channels, requests, err := ssh.NewServerConn(conn, serverConfig)
				if err != nil {
					conn.Close()
					return
				}
				defer server.Close()
				go ssh.DiscardRequests(requests)
				for channel := range channels {
					channel.Reject(ssh.Prohibited, "no forwards needed")
				}
			}()
		}
	}()
	dns, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer dns.Close()
	go func() {
		for {
			buffer := make([]byte, 512)
			n, remote, err := dns.ReadFrom(buffer)
			if err != nil {
				return
			}
			query := append([]byte(nil), buffer[:n]...)
			go func() {
				end := 12
				for end < len(query) && query[end] != 0 {
					end += 1 + int(query[end])
				}
				end++
				if end+4 > len(query) {
					return
				}
				isA := binary.BigEndian.Uint16(query[end:end+2]) == 1
				response := make([]byte, 12)
				copy(response[:2], query[:2])
				binary.BigEndian.PutUint16(response[2:4], 0x8180)
				binary.BigEndian.PutUint16(response[4:6], 1)
				if isA {
					binary.BigEndian.PutUint16(response[6:8], 1)
				}
				response = append(response, query[12:end+4]...)
				if isA {
					response = append(response, 0xc0, 0x0c, 0, 1, 0, 1, 0, 0, 0, 60, 0, 4, 127, 0, 0, 1)
				}
				time.Sleep(1200 * time.Millisecond)
				dns.WriteTo(response, remote)
			}()
		}
	}()
	oldResolver := net.DefaultResolver
	net.DefaultResolver = &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "udp", dns.LocalAddr().String())
	}}
	defer func() { net.DefaultResolver = oldResolver }()
	port := listener.Addr().(*net.TCPAddr).Port
	transport := NewTransport(Object{"host": "127.0.0.1", "port": port, "user": "fixture", "key_file": keyFile,
		"host_public_key": string(ssh.MarshalAuthorizedKey(signer.PublicKey())), "routes": []any{map[string]any{"host": "preferred-route.test"}}})
	defer transport.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err = transport.connect(ctx); err != nil {
		t.Fatal(err)
	}
	if transport.Mode != "ssh:preferred-route.test" {
		t.Fatalf("healthy preferred route abandoned: %s", transport.Mode)
	}
}
