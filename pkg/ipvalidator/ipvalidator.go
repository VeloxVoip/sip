package ipvalidator

import (
	"fmt"
	"net"
	"strings"
)

// IPValidator validates IPs against a list of allowed IPs/CIDRs
type IPValidator struct {
	networks []*net.IPNet
}

// NewIPValidator creates a validator that accepts both CIDR ranges and single IPs
func NewIPValidator(allowed []string) (*IPValidator, error) {
	validator := &IPValidator{
		networks: make([]*net.IPNet, 0, len(allowed)),
	}

	for _, ipStr := range allowed {
		// Normalize the input
		ipStr = strings.TrimSpace(ipStr)
		if ipStr == "" {
			continue
		}

		// If it's not in CIDR format, add /32 for IPv4 or /128 for IPv6
		if !strings.Contains(ipStr, "/") {
			ip := net.ParseIP(ipStr)
			if ip == nil {
				return nil, fmt.Errorf("invalid IP address: %s", ipStr)
			}

			// Determine if IPv4 or IPv6 and add appropriate mask
			if ip.To4() != nil {
				ipStr = ipStr + "/32" // IPv4 single host
			} else {
				ipStr = ipStr + "/128" // IPv6 single host
			}
		}

		// Parse as CIDR
		_, ipNet, err := net.ParseCIDR(ipStr)
		if err != nil {
			return nil, fmt.Errorf("invalid IP/CIDR '%s': %w", ipStr, err)
		}

		validator.networks = append(validator.networks, ipNet)
	}

	return validator, nil
}

// IsAllowed checks if the given IP is in any of the allowed ranges
func (v *IPValidator) IsAllowed(ipStr string) bool {
	// Handle "IP:port" format (e.g., from RemoteAddr)
	host, _, err := net.SplitHostPort(ipStr)
	if err != nil {
		// No port, use the entire string
		host = ipStr
	}

	ip := net.ParseIP(strings.TrimSpace(host))
	if ip == nil {
		return false
	}

	for _, network := range v.networks {
		if network.Contains(ip) {
			return true
		}
	}

	return false
}

// GetNetworks returns the parsed networks (useful for debugging)
func (v *IPValidator) GetNetworks() []string {
	result := make([]string, len(v.networks))
	for i, network := range v.networks {
		result[i] = network.String()
	}
	return result
}

func main() {
	// Example matching your YAML config
	allowedIPs := []string{
		"83.22.40.0/24", // CIDR range
		"192.168.1.100", // Single IP
		"127.0.0.1",     // Another single IP
		"10.0.0.0/8",    // Large range
	}

	fmt.Println("=== Creating IP Validator ===")
	validator, err := NewIPValidator(allowedIPs)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	fmt.Println("Configured networks:")
	for _, net := range validator.GetNetworks() {
		fmt.Printf("  - %s\n", net)
	}

	// Test various IPs
	fmt.Println("\n=== Testing IPs ===")
	testCases := []string{
		"83.22.40.217",       // In range
		"83.22.40.1",         // In range
		"83.22.41.1",         // Out of range
		"192.168.1.100",      // Exact match
		"192.168.1.101",      // Close but not allowed
		"127.0.0.1",          // Exact match
		"10.5.10.50",         // In 10.0.0.0/8 range
		"8.8.8.8",            // Not allowed
		"192.168.1.100:5060", // With port (should work)
	}

	for _, testIP := range testCases {
		allowed := validator.IsAllowed(testIP)
		status := "✗ DENIED"
		if allowed {
			status = "✓ ALLOWED"
		}
		fmt.Printf("%s  %s\n", status, testIP)
	}

	// Example: Load from YAML-like structure
	fmt.Println("\n=== YAML Config Example ===")
	type TrunkConfig struct {
		Name       string
		AllowedIPs []string `yaml:"allowed_ips"`
	}

	trunk := TrunkConfig{
		Name: "production-trunk",
		AllowedIPs: []string{
			"83.22.40.0/24",
			"192.168.1.100",
		},
	}

	trunkValidator, err := NewIPValidator(trunk.AllowedIPs)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	fmt.Printf("Trunk: %s\n", trunk.Name)
	fmt.Println("Testing incoming connection from 83.22.40.217:5060")
	if trunkValidator.IsAllowed("83.22.40.217:5060") {
		fmt.Println("✓ Connection accepted")
	} else {
		fmt.Println("✗ Connection rejected")
	}

	fmt.Println("\nTesting incoming connection from 192.168.1.100:5060")
	if trunkValidator.IsAllowed("192.168.1.100:5060") {
		fmt.Println("✓ Connection accepted")
	} else {
		fmt.Println("✗ Connection rejected")
	}

	fmt.Println("\nTesting incoming connection from 8.8.8.8:5060")
	if trunkValidator.IsAllowed("8.8.8.8:5060") {
		fmt.Println("✓ Connection accepted")
	} else {
		fmt.Println("✗ Connection rejected")
	}

	// Example: IPv6 support
	fmt.Println("\n=== IPv6 Support ===")
	ipv6Allowed := []string{
		"2001:db8::/32",
		"::1", // IPv6 localhost
	}

	ipv6Validator, err := NewIPValidator(ipv6Allowed)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	ipv6Tests := []string{
		"2001:db8::1",
		"::1",
		"2001:db9::1",
	}

	for _, testIP := range ipv6Tests {
		allowed := ipv6Validator.IsAllowed(testIP)
		status := "✗ DENIED"
		if allowed {
			status = "✓ ALLOWED"
		}
		fmt.Printf("%s  %s\n", status, testIP)
	}
}
