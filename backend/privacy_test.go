package main

import "testing"

func TestIngestHashCompatibility(t *testing.T) {
	const k0, k1 = 0x0706050403020100, 0x0f0e0d0c0b0a0908
	if got := ingestSipHash(k0, k1, ""); got != 0x726fdb47dd0e0e31 {
		t.Fatalf("SipHash empty message vector: %016x", got)
	}
	if got := ingestSipHash(k0, k1, "\x00\x01\x02\x03\x04\x05\x06\x07\x08\x09\x0a\x0b\x0c\x0d\x0e"); got != 0xa129ca6149be45e5 {
		t.Fatalf("SipHash fifteen-byte vector: %016x", got)
	}
	salt := [16]byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15}
	if got := ingestUserHash(salt, "1.2.3.4", "agent"); got != "42FB40E20966B4BA" {
		t.Fatalf("legacy hash encoding or key selection changed: %s", got)
	}
}

func TestIngestRedactsPII(t *testing.T) {
	for _, test := range []struct{ input, want string }{
		{"contact x@example.org at 192.168.0.4.", "contact [EMAIL_REDACTED] at [IP_REDACTED]."},
		{"hosts: 192.168.0.4:443 ::ffff:192.0.2.10 fe80::1%en0", "hosts: [IP_REDACTED]:443 [IP_REDACTED] [IP_REDACTED]"},
		{"from:fe80::1", "from:[IP_REDACTED]"},
		{"at std::vector<int>::push_back() 999.999.999.999 1.2.3.4.5", "at std::vector<int>::push_back() 999.999.999.999 1.2.3.4.5"},
		{"(555) 123-4567, card 3782 822463 10005, SSN 123-45-6789", "[PHONE_REDACTED], card [CARD_REDACTED], SSN [SSN_REDACTED]"},
		{`api_key="my_secret_value_1234567890123"; token=eyJaaaa.eyJbbbb.cccc`, `api_key="[KEY_REDACTED]"; token=[JWT_REDACTED]`},
		{`user_id="550e8400-e29b-41d4-a716-446655440000"; error_id=550e8400-e29b-41d4-a716-446655440000`, `user_id=[UUID_REDACTED]; error_id=550e8400-e29b-41d4-a716-446655440000`},
	} {
		if got := ingestSanitizePII(test.input); got != test.want {
			t.Errorf("sanitize %q: got %q, want %q", test.input, got, test.want)
		}
	}
}

func TestIngestRedactsIPBoundaries(t *testing.T) {
	for _, test := range []struct{ input, want string }{
		{"peer:203.0.113.4:443", "peer:[IP_REDACTED]:443"},
		{"at socket:peer:203.0.113.4:443.", "at socket:peer:[IP_REDACTED]:443."},
		{"src:10.0.0.5:80,dst:172.16.0.1:8080", "src:[IP_REDACTED]:80,dst:[IP_REDACTED]:8080"},
		{"Connection to 10.0.0.1.", "Connection to [IP_REDACTED]."},
		{"peer:999.999.999.999:443", "peer:999.999.999.999:443"},
		{"peer:1.2.3.4.5:443", "peer:1.2.3.4.5:443"},
		{"peer:999.1.2.3.4:443", "peer:999.1.2.3.4:443"},
		{"peer:x203.0.113.4:443", "peer:x203.0.113.4:443"},
		{"peer:203.0.113.4x:443", "peer:203.0.113.4x:443"},
		{"IPv6: 2001:0db8:85a3:0000:0000:8a2e:0370:7334", "IPv6: [IP_REDACTED]"},
		{"Localhost: ::1", "Localhost: [IP_REDACTED]"},
		{"MyNamespace::MyClass::Method", "MyNamespace::MyClass::Method"},
	} {
		if got := ingestSanitizePII(test.input); got != test.want {
			t.Errorf("sanitize %q: got %q, want %q", test.input, got, test.want)
		}
	}
}
