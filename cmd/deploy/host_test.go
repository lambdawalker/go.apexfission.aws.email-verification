package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestHostSettingsSavedOverridesAndCI(t *testing.T) {
	d := t.TempDir()
	s, r, domain := "sender@example.com", "recipient@example.com", "example.test"
	if e := hostSettings(d, true, false, &s, &r, &domain); e != nil {
		t.Fatal(e)
	}
	s, r, domain = "", "", ""
	if e := hostSettings(d, true, false, &s, &r, &domain); e != nil || s != "sender@example.com" {
		t.Fatal(e, s)
	}
	r = "new@example.com"
	if e := hostSettings(d, true, false, &s, &r, &domain); e != nil {
		t.Fatal(e)
	}
	os.WriteFile(filepath.Join(d, "testhost.json"), []byte("broken"), 0600)
	if e := hostSettings(d, true, false, &s, &r, &domain); e == nil {
		t.Fatal("corrupt settings accepted")
	}
}
func TestHostSettingsMissingCI(t *testing.T) {
	s, r, d := "", "", ""
	if e := hostSettings(t.TempDir(), true, false, &s, &r, &d); e == nil {
		t.Fatal("missing inputs accepted")
	}
}

func TestTextDefaultAndEOF(t *testing.T) {
	for _, tc := range []struct {
		input, want string
		fail        bool
	}{{"\n", "dev", false}, {"qa\n", "qa", false}, {"", "", true}, {"\x1b\n", "", true}} {
		func() {
			r, w, _ := os.Pipe()
			old := os.Stdin
			os.Stdin = r
			defer func() { os.Stdin = old; r.Close() }()
			w.WriteString(tc.input)
			w.Close()
			defer func() {
				v := recover()
				if (v != nil) != tc.fail {
					t.Errorf("panic=%v wanted failure=%v", v, tc.fail)
				}
			}()
			got := prompt("Environment", "dev")
			if got != tc.want {
				t.Errorf("got %q", got)
			}
		}()
	}
}
