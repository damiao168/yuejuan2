package modelgovernance

import (
	"context"
	"errors"
	"net"
	"testing"
)

func TestManagedProviderRejectsSpecialPurposeAndTransitionAddresses(t *testing.T) {
	for _, value := range []string{
		"0.1.2.3", "100.64.0.1", "100.127.255.254", "192.0.0.9", "192.0.2.1",
		"192.31.196.1", "192.52.193.1", "192.88.99.2", "192.175.48.1", "198.18.0.1",
		"198.19.255.254", "198.51.100.1", "203.0.113.1", "240.0.0.1", "255.255.255.255",
		"::ffff:100.64.0.1", "::ffff:127.0.0.1", "::", "ff02::1", "fc00::1", "fe80::1",
		"64:ff9b::a00:1", "64:ff9b:1::a00:1", "100::1", "100:0:0:1::1", "2001::1",
		"2001:1::1", "2001:2::1", "2001:20::1", "2001:db8::1", "2002:a00:1::1",
		"2620:4f:8000::1", "3fff::1", "5f00::1", "4000::1",
	} {
		t.Run(value, func(t *testing.T) {
			if publicProviderIP(net.ParseIP(value)) {
				t.Fatalf("special-purpose address was accepted: %s", value)
			}
		})
	}
	for _, ip := range []net.IP{nil, {1, 2, 3}} {
		if publicProviderIP(ip) {
			t.Fatalf("malformed address was accepted: %v", ip)
		}
	}
	for _, value := range []string{"8.8.8.8", "1.1.1.1", "::ffff:8.8.8.8", "2001:4860:4860::8888", "2606:4700:4700::1111"} {
		if !publicProviderIP(net.ParseIP(value)) {
			t.Fatalf("ordinary public address was rejected: %s", value)
		}
	}
}

func TestManagedProviderRejectsMixedSpecialDNSBeforeAnyDial(t *testing.T) {
	// 不论特殊地址出现在回答首位还是回退位，都不能先尝试连接公网地址。
	for _, values := range [][]string{{"8.8.8.8", "100.64.0.1"}, {"2002:a00:1::1", "8.8.8.8"}} {
		addresses := []net.IPAddr{{IP: net.ParseIP(values[0])}, {IP: net.ParseIP(values[1])}}
		attempts := 0
		_, err := dialPublicProviderAddresses(context.Background(), "tcp", "443", addresses,
			func(context.Context, string, string) (net.Conn, error) { attempts++; return nil, nil }, nil)
		if !errors.Is(err, ErrInvalidManagedConfig) || attempts != 0 {
			t.Fatalf("mixed special-purpose DNS answer was dialed: values=%v attempts=%d err=%v", values, attempts, err)
		}
	}
}
