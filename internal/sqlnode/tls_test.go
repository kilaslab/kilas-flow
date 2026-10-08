package sqlnode_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/binary"
	"errors"
	"io"
	"math/big"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/kilaslab/kilas-flow/internal/safehttp"
	"github.com/kilaslab/kilas-flow/internal/sqlnode"
)

// untrustedCertificate is a certificate for 127.0.0.1 that no trust store
// holds. The address matches, so the only reason a client can have for
// refusing it is that nobody vouched for it — which is the check under test.
func untrustedCertificate(t *testing.T) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "127.0.0.1"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

// serveTLS stands in for a database that offers TLS with an untrusted
// certificate. preamble speaks whatever the protocol says before the TLS
// handshake; the returned channel reports, per connection, whether the client
// went through with that handshake.
func serveTLS(t *testing.T, preamble func(net.Conn) error) (string, <-chan error) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	certificate := untrustedCertificate(t)
	handshakes := make(chan error, 16)
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
				if err := preamble(conn); err != nil {
					return
				}
				server := tls.Server(conn, &tls.Config{Certificates: []tls.Certificate{certificate}})
				select {
				case handshakes <- server.Handshake():
				default:
				}
			}()
		}
	}()
	_, port, _ := net.SplitHostPort(listener.Addr().String())
	return port, handshakes
}

// postgresPreamble answers PostgreSQL's SSLRequest with S: TLS on offer.
func postgresPreamble(conn net.Conn) error {
	request := make([]byte, 8)
	if _, err := io.ReadFull(conn, request); err != nil {
		return err
	}
	if binary.BigEndian.Uint32(request[4:]) != 80877103 {
		return errors.New("not an SSLRequest")
	}
	_, err := conn.Write([]byte{'S'})
	return err
}

// mysqlPreamble sends a protocol 41 greeting that offers TLS, then reads the
// client's SSL request, after which the client starts the handshake.
func mysqlPreamble(conn net.Conn) error {
	const (
		clientProtocol41 = 0x00000200
		clientSSL        = 0x00000800
		clientSecureConn = 0x00008000
		clientPluginAuth = 0x00080000
	)
	capabilities := uint32(clientProtocol41 | clientSSL | clientSecureConn | clientPluginAuth)
	payload := []byte{10}
	payload = append(payload, "8.0.0\x00"...)
	payload = append(payload, 1, 0, 0, 0)    // connection id
	payload = append(payload, "abcdefgh"...) // scramble, first part
	payload = append(payload, 0)             // filler
	payload = binary.LittleEndian.AppendUint16(payload, uint16(capabilities))
	payload = append(payload, 45)   // character set
	payload = append(payload, 2, 0) // status
	payload = binary.LittleEndian.AppendUint16(payload, uint16(capabilities>>16))
	payload = append(payload, 21)                    // scramble length
	payload = append(payload, make([]byte, 10)...)   // reserved
	payload = append(payload, "ijklmnopqrst\x00"...) // scramble, second part
	payload = append(payload, "mysql_native_password\x00"...)
	packet := []byte{byte(len(payload)), byte(len(payload) >> 8), byte(len(payload) >> 16), 0}
	if _, err := conn.Write(append(packet, payload...)); err != nil {
		return err
	}
	header := make([]byte, 4)
	if _, err := io.ReadFull(conn, header); err != nil {
		return err
	}
	_, err := io.ReadFull(conn, make([]byte, int(header[0])|int(header[1])<<8|int(header[2])<<16))
	return err
}

// A database credential that says nothing about TLS verifies the server's
// certificate, on both network drivers; the explicit opt-outs still encrypt
// without verifying.
func TestADatabaseCredentialVerifiesTheServerCertificateByDefault(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name     string
		driver   sqlnode.Driver
		preamble func(net.Conn) error
		field    string
		optOut   string
	}{
		{"postgres", sqlnode.DriverPostgres, postgresPreamble, "sslMode", "require"},
		{"mysql", sqlnode.DriverMySQL, mysqlPreamble, "tls", "skip-verify"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			open := func(t *testing.T, mode string) (error, error) {
				t.Helper()
				port, handshakes := serveTLS(t, test.preamble)
				fields := map[string]string{
					"host": "127.0.0.1", "port": port, "database": "app",
					"user": "ada", "password": "hunter2",
				}
				if mode != "" {
					fields[test.field] = mode
				}
				guard := sqlnode.Guard{Policy: safehttp.Policy{AllowedPrivateEndpoints: []string{"127.0.0.1:" + port}}}
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				_, err := sqlnode.Open(ctx, test.driver, fields, guard)
				select {
				case handshake := <-handshakes:
					return err, handshake
				case <-time.After(5 * time.Second):
					t.Fatalf("the client never reached the TLS handshake: %v", err)
					return nil, nil
				}
			}

			t.Run("default", func(t *testing.T) {
				err, handshake := open(t, "")
				if handshake == nil {
					t.Fatal("the client completed a TLS handshake with a certificate nobody signed")
				}
				var unverified *tls.CertificateVerificationError
				if !errors.As(err, &unverified) {
					t.Fatalf("Open() error = %v, want a certificate verification error", err)
				}
				// The refusal names the way out, because a server with a
				// self-signed certificate used to connect without one.
				if !strings.Contains(err.Error(), test.optOut) {
					t.Errorf("Open() error = %v, want it to name %s=%s", err, test.field, test.optOut)
				}
			})

			t.Run("explicit opt-out", func(t *testing.T) {
				_, handshake := open(t, test.optOut)
				if handshake != nil {
					t.Fatalf("%s=%s refused the certificate: %v", test.field, test.optOut, handshake)
				}
			})
		})
	}
}
