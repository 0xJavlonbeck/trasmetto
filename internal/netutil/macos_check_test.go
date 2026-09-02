package netutil

import (
	"crypto/rsa"
	"crypto/x509"
	"testing"
)

func TestSelfSignedCertificateMeetsAppleRequirements(t *testing.T) {
	cert, err := SelfSignedCertificate("0.0.0.0")
	if err != nil {
		t.Fatalf("SelfSignedCertificate: %v", err)
	}
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	if days := leaf.NotAfter.Sub(leaf.NotBefore).Hours() / 24; days > 398 {
		t.Errorf("validity = %.0f days, want <= 398 (Apple limit)", days)
	}

	if k, ok := leaf.PublicKey.(*rsa.PublicKey); ok && k.N.BitLen() < 2048 {
		t.Errorf("RSA key = %d bits, want >= 2048", k.N.BitLen())
	}

	if len(leaf.IPAddresses)+len(leaf.DNSNames) == 0 {
		t.Error("certificate has no SAN entries; Apple ignores CommonName")
	}

	switch leaf.SignatureAlgorithm {
	case x509.SHA1WithRSA, x509.DSAWithSHA1, x509.ECDSAWithSHA1:
		t.Errorf("signature algorithm %v is rejected by Apple platforms", leaf.SignatureAlgorithm)
	}

	hasServerAuth := false
	for _, eku := range leaf.ExtKeyUsage {
		if eku == x509.ExtKeyUsageServerAuth {
			hasServerAuth = true
		}
	}
	if !hasServerAuth {
		t.Error("certificate lacks ExtKeyUsageServerAuth")
	}
}
