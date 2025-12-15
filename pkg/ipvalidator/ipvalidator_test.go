package ipvalidator_test

import (
	"testing"

	"github.com/veloxvoip/sip/pkg/ipvalidator"
)

func TestNewIPValidator(t *testing.T) {
	tests := []struct {
		name      string
		allowed   []string
		wantErr   bool
		errString string
	}{
		{
			name:    "valid CIDR ranges",
			allowed: []string{"192.168.1.0/24", "10.0.0.0/8"},
			wantErr: false,
		},
		{
			name:    "valid single IPs",
			allowed: []string{"192.168.1.100", "10.0.0.1"},
			wantErr: false,
		},
		{
			name:    "mixed CIDR and single IPs",
			allowed: []string{"192.168.1.0/24", "10.0.0.1", "83.22.40.0/24"},
			wantErr: false,
		},
		{
			name:    "empty list",
			allowed: []string{},
			wantErr: false,
		},
		{
			name:    "whitespace entries",
			allowed: []string{"  192.168.1.0/24  ", "  ", "10.0.0.1"},
			wantErr: false,
		},
		{
			name:      "invalid IP address",
			allowed:   []string{"999.999.999.999"},
			wantErr:   true,
			errString: "invalid IP address",
		},
		{
			name:      "invalid CIDR notation",
			allowed:   []string{"192.168.1.0/99"},
			wantErr:   true,
			errString: "invalid IP/CIDR",
		},
		{
			name:      "malformed CIDR",
			allowed:   []string{"192.168.1/24"},
			wantErr:   true,
			errString: "invalid IP/CIDR",
		},
		{
			name:    "IPv6 CIDR",
			allowed: []string{"2001:db8::/32"},
			wantErr: false,
		},
		{
			name:    "IPv6 single address",
			allowed: []string{"::1", "2001:db8::1"},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			validator, err := ipvalidator.NewIPValidator(tt.allowed)
			if tt.wantErr {
				if err == nil {
					t.Errorf("NewIPValidator() expected error but got none")
				}
				return
			}
			if err != nil {
				t.Errorf("NewIPValidator() unexpected error: %v", err)
				return
			}
			if validator == nil {
				t.Errorf("NewIPValidator() returned nil validator")
			}
		})
	}
}

func TestIPValidator_IsAllowed(t *testing.T) {
	tests := []struct {
		name    string
		allowed []string
		testIP  string
		want    bool
	}{
		// CIDR range tests
		{
			name:    "IP in CIDR range",
			allowed: []string{"192.168.1.0/24"},
			testIP:  "192.168.1.50",
			want:    true,
		},
		{
			name:    "IP at start of CIDR range",
			allowed: []string{"192.168.1.0/24"},
			testIP:  "192.168.1.1",
			want:    true,
		},
		{
			name:    "IP at end of CIDR range",
			allowed: []string{"192.168.1.0/24"},
			testIP:  "192.168.1.254",
			want:    true,
		},
		{
			name:    "IP outside CIDR range",
			allowed: []string{"192.168.1.0/24"},
			testIP:  "192.168.2.1",
			want:    false,
		},

		// Single IP tests
		{
			name:    "exact IP match",
			allowed: []string{"192.168.1.100"},
			testIP:  "192.168.1.100",
			want:    true,
		},
		{
			name:    "single IP no match",
			allowed: []string{"192.168.1.100"},
			testIP:  "192.168.1.101",
			want:    false,
		},

		// Mixed tests
		{
			name:    "match first in list",
			allowed: []string{"10.0.0.0/8", "192.168.1.0/24", "172.16.0.1"},
			testIP:  "10.5.10.50",
			want:    true,
		},
		{
			name:    "match middle in list",
			allowed: []string{"10.0.0.0/8", "192.168.1.0/24", "172.16.0.1"},
			testIP:  "192.168.1.50",
			want:    true,
		},
		{
			name:    "match last in list",
			allowed: []string{"10.0.0.0/8", "192.168.1.0/24", "172.16.0.1"},
			testIP:  "172.16.0.1",
			want:    true,
		},
		{
			name:    "no match in list",
			allowed: []string{"10.0.0.0/8", "192.168.1.0/24", "172.16.0.1"},
			testIP:  "8.8.8.8",
			want:    false,
		},

		// Port handling tests
		{
			name:    "IP with port in range",
			allowed: []string{"192.168.1.0/24"},
			testIP:  "192.168.1.50:5060",
			want:    true,
		},
		{
			name:    "IP with port exact match",
			allowed: []string{"192.168.1.100"},
			testIP:  "192.168.1.100:5060",
			want:    true,
		},
		{
			name:    "IP with port not in range",
			allowed: []string{"192.168.1.0/24"},
			testIP:  "192.168.2.50:5060",
			want:    false,
		},

		// Edge cases
		{
			name:    "localhost",
			allowed: []string{"127.0.0.1"},
			testIP:  "127.0.0.1",
			want:    true,
		},
		{
			name:    "empty allowed list",
			allowed: []string{},
			testIP:  "192.168.1.1",
			want:    false,
		},
		{
			name:    "invalid IP format",
			allowed: []string{"192.168.1.0/24"},
			testIP:  "invalid-ip",
			want:    false,
		},
		{
			name:    "empty IP string",
			allowed: []string{"192.168.1.0/24"},
			testIP:  "",
			want:    false,
		},

		// Large CIDR blocks
		{
			name:    "large /8 block",
			allowed: []string{"10.0.0.0/8"},
			testIP:  "10.255.255.255",
			want:    true,
		},
		{
			name:    "/32 single host",
			allowed: []string{"192.168.1.100/32"},
			testIP:  "192.168.1.100",
			want:    true,
		},
		{
			name:    "/32 single host no match",
			allowed: []string{"192.168.1.100/32"},
			testIP:  "192.168.1.101",
			want:    false,
		},

		// IPv6 tests
		{
			name:    "IPv6 in range",
			allowed: []string{"2001:db8::/32"},
			testIP:  "2001:db8::1",
			want:    true,
		},
		{
			name:    "IPv6 out of range",
			allowed: []string{"2001:db8::/32"},
			testIP:  "2001:db9::1",
			want:    false,
		},
		{
			name:    "IPv6 localhost",
			allowed: []string{"::1"},
			testIP:  "::1",
			want:    true,
		},
		{
			name:    "IPv6 with port",
			allowed: []string{"2001:db8::/32"},
			testIP:  "[2001:db8::1]:5060",
			want:    true,
		},

		// Real-world example
		{
			name:    "VoIP trunk config",
			allowed: []string{"83.22.40.0/24", "192.168.1.100"},
			testIP:  "83.22.40.217",
			want:    true,
		},
		{
			name:    "VoIP trunk config exact IP",
			allowed: []string{"83.22.40.0/24", "192.168.1.100"},
			testIP:  "192.168.1.100",
			want:    true,
		},
		{
			name:    "VoIP trunk config with port",
			allowed: []string{"83.22.40.0/24", "192.168.1.100"},
			testIP:  "83.22.40.217:5060",
			want:    true,
		},
		{
			name:    "VoIP trunk config denied",
			allowed: []string{"83.22.40.0/24", "192.168.1.100"},
			testIP:  "8.8.8.8",
			want:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			validator, err := ipvalidator.NewIPValidator(tt.allowed)
			if err != nil {
				t.Fatalf("NewIPValidator() error: %v", err)
			}

			got := validator.IsAllowed(tt.testIP)
			if got != tt.want {
				t.Errorf("IsAllowed(%q) = %v, want %v", tt.testIP, got, tt.want)
			}
		})
	}
}

func TestIPValidator_GetNetworks(t *testing.T) {
	tests := []struct {
		name      string
		allowed   []string
		wantCount int
	}{
		{
			name:      "single CIDR",
			allowed:   []string{"192.168.1.0/24"},
			wantCount: 1,
		},
		{
			name:      "multiple entries",
			allowed:   []string{"192.168.1.0/24", "10.0.0.1", "172.16.0.0/16"},
			wantCount: 3,
		},
		{
			name:      "empty list",
			allowed:   []string{},
			wantCount: 0,
		},
		{
			name:      "single IP converts to /32",
			allowed:   []string{"192.168.1.100"},
			wantCount: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			validator, err := ipvalidator.NewIPValidator(tt.allowed)
			if err != nil {
				t.Fatalf("NewIPValidator() error: %v", err)
			}

			networks := validator.GetNetworks()
			if len(networks) != tt.wantCount {
				t.Errorf("GetNetworks() returned %d networks, want %d", len(networks), tt.wantCount)
			}
		})
	}
}

// Benchmark tests
func BenchmarkIPValidator_IsAllowed(b *testing.B) {
	allowed := []string{
		"10.0.0.0/8",
		"172.16.0.0/12",
		"192.168.0.0/16",
		"83.22.40.0/24",
		"127.0.0.1",
	}

	validator, err := ipvalidator.NewIPValidator(allowed)
	if err != nil {
		b.Fatalf("NewIPValidator() error: %v", err)
	}

	testIP := "192.168.1.50"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		validator.IsAllowed(testIP)
	}
}

func BenchmarkIPValidator_IsAllowedWithPort(b *testing.B) {
	allowed := []string{
		"10.0.0.0/8",
		"172.16.0.0/12",
		"192.168.0.0/16",
	}

	validator, err := ipvalidator.NewIPValidator(allowed)
	if err != nil {
		b.Fatalf("NewIPValidator() error: %v", err)
	}

	testIP := "192.168.1.50:5060"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		validator.IsAllowed(testIP)
	}
}

func BenchmarkNewIPValidator(b *testing.B) {
	allowed := []string{
		"10.0.0.0/8",
		"172.16.0.0/12",
		"192.168.0.0/16",
		"83.22.40.0/24",
		"127.0.0.1",
		"192.168.1.100",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = ipvalidator.NewIPValidator(allowed)
	}
}
