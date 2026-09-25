package buildinfo

import "testing"

func TestVersionDefaultsToDevelopment(t *testing.T) {
	if Version == "" {
		t.Fatal("Version must not be empty")
	}
}

func TestBuildMetadataDefaults(t *testing.T) {
	tests := []struct {
		name string
		got  string
		want string
	}{
		{name: "version", got: Version, want: "dev"},
		{name: "commit", got: Commit, want: "none"},
		{name: "build date", got: BuildDate, want: "unknown"},
	}

	for _, test := range tests {
		if test.got != test.want {
			t.Errorf("%s = %q, want %q", test.name, test.got, test.want)
		}
	}
}
