// Package plugininfra 实现 Plugin 系统的基础设施：市场加载、安装器、状态持久化。
//
// 安全要点：
//   - 远程来源（git/http）在访问前做 SSRF 校验，拒绝内网地址；
//   - 压缩包解压做 zip-slip / 路径穿越防护；
//   - 限制下载大小与超时；
//   - git 通过参数数组调用，禁止 shell 字符串拼接。
package plugininfra

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

// 下载与解压的硬性上限，防止资源耗尽。
const (
	maxDownloadBytes = 100 << 20 // 100 MiB
	maxArchiveFiles  = 20000
	maxArchiveBytes  = 500 << 20 // 解压后总大小上限 500 MiB
)

// allowInternalHosts 控制是否放行内网地址访问；默认 false。
// 由调用方在用户显式授权后置 true（对齐安全规则中的 SSRF 例外）。
var allowInternalHosts = false

// SetAllowInternalHosts 设置是否允许访问内网地址（用户显式授权时）。
func SetAllowInternalHosts(allow bool) { allowInternalHosts = allow }

// validateRemoteURL 校验一个 http/https/git URL 的主机是否安全（非内网）。
//
// 返回 error 时表示该地址被拒绝。allowInternalHosts=true 时跳过内网检查。
func validateRemoteURL(rawURL string) error {
	host := extractHost(rawURL)
	if host == "" {
		return fmt.Errorf("无法解析来源主机: %q", rawURL)
	}
	return validateHost(host)
}

// extractHost 从 URL 或 scp 风格 git 地址（git@host:path）中提取主机名。
func extractHost(raw string) string {
	s := strings.TrimSpace(raw)
	s = strings.TrimPrefix(s, "git+")
	// scp 风格: git@host:owner/repo.git
	if strings.HasPrefix(s, "git@") {
		rest := strings.TrimPrefix(s, "git@")
		if i := strings.IndexByte(rest, ':'); i > 0 {
			return rest[:i]
		}
		return rest
	}
	u, err := url.Parse(s)
	if err != nil {
		return ""
	}
	return u.Hostname()
}

// validateHost 校验主机名/IP 是否允许访问。
//
// 拒绝：环回、链路本地、私有网段、未指定地址，以及安全规则要求的
// 9.* / 10.* / 11.* / 21.* / 30.* 段。
func validateHost(host string) error {
	if allowInternalHosts {
		return nil
	}
	host = strings.TrimSpace(host)
	if host == "" {
		return fmt.Errorf("空主机名")
	}
	// 直接是 IP
	if ip := net.ParseIP(host); ip != nil {
		return validateIP(ip)
	}
	// 域名：解析所有 A/AAAA 记录，任一命中内网即拒绝
	ips, err := net.LookupIP(host)
	if err != nil {
		return fmt.Errorf("解析主机 %q 失败: %w", host, err)
	}
	for _, ip := range ips {
		if err := validateIP(ip); err != nil {
			return fmt.Errorf("主机 %q 指向受限地址: %w", host, err)
		}
	}
	return nil
}

// blockedV4Prefixes 安全规则额外要求拒绝的 IPv4 首字节段。
var blockedV4Prefixes = map[byte]bool{
	9:  true,
	10: true,
	11: true,
	21: true,
	30: true,
}

func validateIP(ip net.IP) error {
	if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsPrivate() || ip.IsUnspecified() || ip.IsMulticast() {
		return fmt.Errorf("内网/保留地址被拒绝: %s", ip)
	}
	if v4 := ip.To4(); v4 != nil {
		if blockedV4Prefixes[v4[0]] {
			return fmt.Errorf("受限网段被拒绝: %s", ip)
		}
	}
	return nil
}
