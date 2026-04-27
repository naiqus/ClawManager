package services

import "testing"

func TestDefaultImagePullPolicy_Default(t *testing.T) {
	// With no env var set, should return "IfNotPresent".
	t.Setenv("IMAGE_PULL_POLICY", "")
	got := defaultImagePullPolicy()
	if got != "IfNotPresent" {
		t.Fatalf("expected IfNotPresent, got %q", got)
	}
}

func TestDefaultImagePullPolicy_EnvOverride(t *testing.T) {
	cases := []struct {
		env  string
		want string
	}{
		{"Always", "Always"},
		{"Never", "Never"},
		{"IfNotPresent", "IfNotPresent"},
	}
	for _, tc := range cases {
		t.Run(tc.env, func(t *testing.T) {
			t.Setenv("IMAGE_PULL_POLICY", tc.env)
			got := defaultImagePullPolicy()
			if got != tc.want {
				t.Fatalf("expected %q, got %q", tc.want, got)
			}
		})
	}
}

func TestDefaultImagePullPolicy_WhitespaceOnly(t *testing.T) {
	// Whitespace-only env var should fall back to default.
	t.Setenv("IMAGE_PULL_POLICY", "   ")
	got := defaultImagePullPolicy()
	if got != "IfNotPresent" {
		t.Fatalf("expected IfNotPresent, got %q", got)
	}
}

