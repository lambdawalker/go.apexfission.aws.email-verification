package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
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
	}{{"\n", "dev", false}, {"qa\n", "qa", false}, {"", "", true}, {"qa", "", true}, {"\x1b\n", "", true}} {
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

func TestTextInputDisarmsCountdownBeforeEnter(t *testing.T) {
	old := countdownStep
	countdownStep = 200 * time.Millisecond
	t.Cleanup(func() { countdownStep = old })

	input, keys, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	defer keys.Close()
	if _, err := keys.WriteString("q"); err != nil {
		t.Fatal(err)
	}
	output, err := os.CreateTemp(t.TempDir(), "prompt-output")
	if err != nil {
		t.Fatal(err)
	}
	defer output.Close()

	// Enter arrives after the original timeout, so a line-based timer would
	// return the default instead of waiting for the rest of the override.
	type result struct {
		value string
		err   error
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	done := make(chan result, 1)
	go func() {
		value, err := readTextPrompt(ctx, input, output, "Environment", "dev")
		done <- result{value, err}
	}()
	select {
	case got := <-done:
		t.Fatalf("prompt finished before Enter: %+v", got)
	case <-time.After(6 * countdownStep):
	}
	if _, err := keys.WriteString("a\n"); err != nil {
		t.Fatal(err)
	}
	got := <-done
	if got.err != nil || got.value != "qa" {
		t.Fatalf("prompt = %q, %v; want qa", got.value, got.err)
	}
}
