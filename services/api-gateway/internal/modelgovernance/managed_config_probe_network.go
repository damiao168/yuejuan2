package modelgovernance

import (
	"net"
	"net/netip"
)

// 探测仅接受普通公网服务地址；IANA 特殊用途网段即使可全球路由，也不作为模型提供商端点。
// 网段依据见 reports/codeql-remediation-2026-10-11/go-remediation.md；注册表变化时需同步核对。
var managedProviderSpecialPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("10.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("127.0.0.0/8"),
	netip.MustParsePrefix("169.254.0.0/16"),
	netip.MustParsePrefix("172.16.0.0/12"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("192.31.196.0/24"),
	netip.MustParsePrefix("192.52.193.0/24"),
	netip.MustParsePrefix("192.88.99.0/24"),
	netip.MustParsePrefix("192.168.0.0/16"),
	netip.MustParsePrefix("192.175.48.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("2001::/23"),
	netip.MustParsePrefix("2001:db8::/32"),
	netip.MustParsePrefix("2002::/16"),
	netip.MustParsePrefix("2620:4f:8000::/48"),
	netip.MustParsePrefix("3fff::/20"),
}

var managedProviderIPv6GlobalPrefix = netip.MustParsePrefix("2000::/3")

func publicProviderIP(ip net.IP) bool {
	address, ok := netip.AddrFromSlice(ip)
	if !ok {
		return false
	}
	// IPv4 映射地址按其真实 IPv4 网段检查，不能用 IPv6 写法绕过共享地址段等限制。
	address = address.Unmap()
	if !address.IsGlobalUnicast() || address.IsPrivate() {
		return false
	}
	// IPv6 只允许当前公网单播空间；同时阻断 NAT64 等可嵌入内网 IPv4 的转换前缀。
	if address.Is6() && !managedProviderIPv6GlobalPrefix.Contains(address) {
		return false
	}
	for _, prefix := range managedProviderSpecialPrefixes {
		if prefix.Contains(address) {
			return false
		}
	}
	return true
}
