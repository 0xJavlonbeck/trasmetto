package netutil

import (
	"crypto/x509"
	"strings"
	"testing"
	"time"
)

func leafOf(t *testing.T, host string) *x509.Certificate {
	t.Helper()
	cert, err := SelfSignedCertificate(host)
	if err != nil {
		t.Fatalf("SelfSignedCertificate(%q): %v", host, err)
	}
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		t.Fatalf("parse leaf: %v", err)
	}
	return leaf
}

func TestSelfSignedCertificateProperties(t *testing.T) {
	leaf := leafOf(t, "127.0.0.1")

	if leaf.Subject.CommonName != "Trasmetto" {
		t.Errorf("CommonName = %q, want Trasmetto", leaf.Subject.CommonName)
	}
	if leaf.IsCA {
		t.Error("certificate is a CA, want leaf (CA:FALSE)")
	}
	if !leaf.BasicConstraintsValid {
		t.Error("BasicConstraintsValid = false")
	}
	if leaf.KeyUsage&x509.KeyUsageDigitalSignature == 0 || leaf.KeyUsage&x509.KeyUsageKeyEncipherment == 0 {
		t.Errorf("KeyUsage missing digital signature / key encipherment: %v", leaf.KeyUsage)
	}
	if len(leaf.ExtKeyUsage) != 1 || leaf.ExtKeyUsage[0] != x509.ExtKeyUsageServerAuth {
		t.Errorf("ExtKeyUsage = %v, want [ServerAuth]", leaf.ExtKeyUsage)
	}

	if !leaf.NotBefore.Before(time.Now()) {
		t.Error("NotBefore is not in the past")
	}
	wantAfter := time.Now().AddDate(1, 0, 0)
	if diff := leaf.NotAfter.Sub(wantAfter); diff > time.Hour || diff < -time.Hour {
		t.Errorf("NotAfter = %v, want ~%v", leaf.NotAfter, wantAfter)
	}
}

func TestSelfSignedCertificateSpecificHostSAN(t *testing.T) {
	leaf := leafOf(t, "127.0.0.1")

	if len(leaf.IPAddresses) != 1 || leaf.IPAddresses[0].String() != "127.0.0.1" {
		t.Fatalf("IP SANs = %v, want [127.0.0.1] only", leaf.IPAddresses)
	}

	for _, name := range leaf.DNSNames {
		if name == "localhost" {
			t.Errorf("specific host cert unexpectedly includes DNS SAN %q", name)
		}
	}
	if err := leaf.VerifyHostname("127.0.0.1"); err != nil {
		t.Errorf("VerifyHostname(127.0.0.1): %v", err)
	}
}

func TestSelfSignedCertificateUnspecifiedHostSAN(t *testing.T) {
	for _, host := range []string{"", "0.0.0.0", "::"} {
		leaf := leafOf(t, host)

		if err := leaf.VerifyHostname("localhost"); err != nil {
			t.Errorf("host %q: VerifyHostname(localhost): %v", host, err)
		}
		if err := leaf.VerifyHostname("127.0.0.1"); err != nil {
			t.Errorf("host %q: VerifyHostname(127.0.0.1): %v", host, err)
		}
		hasLoopbackV6 := false
		for _, ip := range leaf.IPAddresses {
			if ip.String() == "::1" {
				hasLoopbackV6 = true
			}
		}
		if !hasLoopbackV6 {
			t.Errorf("host %q: ::1 missing from IP SANs %v", host, leaf.IPAddresses)
		}
	}
}

func TestReachableURLsSpecificHost(t *testing.T) {
	if got := ReachableURLs("192.168.1.5", "http", 8000); got != nil {
		t.Errorf("ReachableURLs(specific host) = %v, want nil", got)
	}
	if got := ReachableURLs("127.0.0.1", "https", 443); got != nil {
		t.Errorf("ReachableURLs(loopback) = %v, want nil", got)
	}
}

func TestReachableURLsUnspecifiedHost(t *testing.T) {
	for _, host := range []string{"", "0.0.0.0", "::"} {
		urls := ReachableURLs(host, "https", 8443)
		if len(urls) == 0 {
			t.Fatalf("host %q: got no URLs", host)
		}
		if urls[0] != "https://127.0.0.1:8443/" {
			t.Errorf("host %q: first URL = %q, want https://127.0.0.1:8443/", host, urls[0])
		}
		for _, u := range urls {
			if !strings.HasPrefix(u, "https://") || !strings.HasSuffix(u, ":8443/") {
				t.Errorf("host %q: malformed URL %q", host, u)
			}
		}
	}
}

func TestIsUnspecifiedHost(t *testing.T) {
	cases := map[string]bool{
		"":            true,
		"0.0.0.0":     true,
		"::":          true,
		"127.0.0.1":   false,
		"192.168.1.5": false,
		"localhost":   false,
		"not-an-ip":   false,
	}
	for host, want := range cases {
		if got := isUnspecifiedHost(host); got != want {
			t.Errorf("isUnspecifiedHost(%q) = %v, want %v", host, got, want)
		}
	}
}
