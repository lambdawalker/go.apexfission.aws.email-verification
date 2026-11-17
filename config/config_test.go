package config

import (
	"strings"
	"testing"
	"time"
)

func TestDefaultAndInvalidPolicies(t *testing.T) {
	if e := Defaults().Validate(); e != nil {
		t.Fatal(e)
	}
	for _, raw := range []string{`{"retention_seconds":1}`, `{"refresh_seconds":900}`, `{"attempts":0}`, `{"start_seconds":9223372036854775807}`, `{"typo":1}`, `{"proof_seconds":1.5}`, `{} {}`, `null`, `{"attempts":null}`, `{"attempts":3,"attempts":5}`} {
		if _, e := Load(strings.NewReader(raw)); e == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
}
func TestIndependentClocks(t *testing.T) {
	c := Defaults()
	c.AssertionSeconds = 1200
	c.KeyOverlapSeconds = 1203
	if e := c.Validate(); e != nil {
		t.Fatal("max age may precede assertion expiration", e)
	}
}
func TestCleanup(t *testing.T) {
	n := time.Unix(1800000000, 0)
	if _, e := CleanupAt(time.Time{}, n, n, 172800); e == nil {
		t.Fatal("unresolved")
	}
	end := n.Add(3 * 24 * time.Hour)
	got, e := CleanupAt(n, n, end, 172800)
	if e != nil || !got.Equal(end) {
		t.Fatal(got, e)
	}
}
