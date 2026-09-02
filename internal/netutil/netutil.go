package netutil

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"strconv"
	"time"
)

func SelfSignedCertificate(host string) (tls.Certificate, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return tls.Certificate{}, err
	}

	serialLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serial, err := rand.Int(rand.Reader, serialLimit)
	if err != nil {
		return tls.Certificate{}, err
	}

	now := time.Now()
	template := x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName: "Trasmetto",
		},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.AddDate(1, 0, 0),
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}

	addCertificateHosts(&template, host)

	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, err
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	return tls.X509KeyPair(certPEM, keyPEM)
}

func addCertificateHosts(cert *x509.Certificate, host string) {
	addHost := func(value string) {
		if value == "" {
			return
		}
		if ip := net.ParseIP(value); ip != nil {
			cert.IPAddresses = append(cert.IPAddresses, ip)
			return
		}
		cert.DNSNames = append(cert.DNSNames, value)
	}

	addHost(host)
	if isUnspecifiedHost(host) {
		addHost("localhost")
		addHost("127.0.0.1")
		addHost("::1")
		for _, ip := range localInterfaceIPs() {
			addHost(ip.String())
		}
	}
}

func ReachableURLs(host, scheme string, port int) []string {
	if !isUnspecifiedHost(host) {
		return nil
	}

	hosts := []string{"127.0.0.1"}
	for _, ip := range localInterfaceIPs() {
		if ip.IsLinkLocalUnicast() {
			continue
		}
		hosts = append(hosts, ip.String())
	}

	urls := make([]string, 0, len(hosts))
	for _, h := range hosts {
		urls = append(urls, scheme+"://"+net.JoinHostPort(h, strconv.Itoa(port))+"/")
	}
	return urls
}

func isUnspecifiedHost(host string) bool {
	if host == "" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsUnspecified()
}

func localInterfaceIPs() []net.IP {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil
	}

	ips := make([]net.IP, 0, len(addrs))
	seen := make(map[string]bool)
	for _, addr := range addrs {
		var ip net.IP
		switch value := addr.(type) {
		case *net.IPNet:
			ip = value.IP
		case *net.IPAddr:
			ip = value.IP
		}
		if ip == nil || ip.IsLoopback() || ip.IsUnspecified() {
			continue
		}
		key := ip.String()
		if seen[key] {
			continue
		}
		seen[key] = true
		ips = append(ips, ip)
	}
	return ips
}
