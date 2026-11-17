// Package config validates effective timing policy before deployment and startup.
package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"
)

// All durations are integer seconds. Load overlays explicit values on Defaults.
type Config struct {
	StartSeconds           int64 `json:"start_seconds"`
	ProofSeconds           int64 `json:"proof_seconds"`
	AssertionSeconds       int64 `json:"assertion_seconds"`
	VerificationAgeSeconds int64 `json:"verification_age_seconds"`
	ToleranceSeconds       int64 `json:"tolerance_seconds"`
	StatusSeconds          int64 `json:"status_seconds"`
	RetentionSeconds       int64 `json:"retention_seconds"`
	RetryWindowSeconds     int64 `json:"retry_window_seconds"`
	RefreshSeconds         int64 `json:"refresh_seconds"`
	CooldownSeconds        int64 `json:"cooldown_seconds"`
	MaxCacheSeconds        int64 `json:"max_cache_seconds"`
	BackoffSeconds         int64 `json:"backoff_seconds"`
	MaxBackoffSeconds      int64 `json:"max_backoff_seconds"`
	TimeoutSeconds         int64 `json:"timeout_seconds"`
	Attempts               int   `json:"attempts"`
	ProofAttempts          int   `json:"proof_attempts"`
	RotationSeconds        int64 `json:"rotation_seconds"`
	PublicationSeconds     int64 `json:"publication_seconds"`
	KeyOverlapSeconds      int64 `json:"key_overlap_seconds"`
	RetiredMonths          int   `json:"retired_months"`
	DeletionWaitDays       int   `json:"deletion_wait_days"`
}

func Defaults() Config {
	return Config{300, 600, 600, 900, 3, 1800, 172800, 86400, 300, 30, 900, 1, 30, 10, 5, 5, 365 * 86400, 7 * 86400, 603, 3, 30}
}
func Load(r io.Reader) (Config, error) {
	c := Defaults()
	raw, e := io.ReadAll(io.LimitReader(r, 65537))
	if e != nil {
		return c, e
	}
	if len(raw) > 65536 {
		return c, errors.New("configuration too large")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	t, e := d.Token()
	if e != nil || t != json.Delim('{') {
		return c, errors.New("configuration must be an object")
	}
	seen := map[string]bool{}
	for d.More() {
		k, e := d.Token()
		if e != nil {
			return c, e
		}
		name := k.(string)
		if seen[name] {
			return c, fmt.Errorf("duplicate configuration field %s", name)
		}
		seen[name] = true
		var v json.RawMessage
		if e = d.Decode(&v); e != nil {
			return c, e
		}
		if bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
			return c, fmt.Errorf("%s cannot be null", name)
		}
	}
	if _, e = d.Token(); e != nil {
		return c, e
	}
	if _, e = d.Token(); e != io.EOF {
		return c, errors.New("configuration must contain one object")
	}
	d = json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if e = d.Decode(&c); e != nil {
		return c, e
	}
	return c, c.Validate()
}
func (c Config) Validate() error {
	var es []error
	positive := map[string]int64{"start_seconds": c.StartSeconds, "proof_seconds": c.ProofSeconds, "assertion_seconds": c.AssertionSeconds, "verification_age_seconds": c.VerificationAgeSeconds, "status_seconds": c.StatusSeconds, "retention_seconds": c.RetentionSeconds, "retry_window_seconds": c.RetryWindowSeconds, "refresh_seconds": c.RefreshSeconds, "cooldown_seconds": c.CooldownSeconds, "max_cache_seconds": c.MaxCacheSeconds, "backoff_seconds": c.BackoffSeconds, "max_backoff_seconds": c.MaxBackoffSeconds, "timeout_seconds": c.TimeoutSeconds, "rotation_seconds": c.RotationSeconds, "publication_seconds": c.PublicationSeconds, "key_overlap_seconds": c.KeyOverlapSeconds}
	for k, v := range positive {
		if v <= 0 || v > 315360000 {
			es = append(es, fmt.Errorf("%s=%d: require 1..315360000 seconds", k, v))
		}
	}
	if c.ToleranceSeconds < 0 || c.ToleranceSeconds > 60 {
		es = append(es, fmt.Errorf("tolerance_seconds=%d: require 0..60", c.ToleranceSeconds))
	}
	check := func(ok bool, msg string) {
		if !ok {
			es = append(es, errors.New(msg))
		}
	}
	check(c.Attempts >= 1 && c.Attempts <= 100, "attempts must be 1..100, including first attempt")
	check(c.ProofAttempts >= 1 && c.ProofAttempts <= 20, "proof_attempts must be 1..20")
	check(c.RefreshSeconds < c.MaxCacheSeconds, "refresh_seconds must be below max_cache_seconds")
	check(c.CooldownSeconds <= c.RefreshSeconds, "cooldown_seconds must not exceed refresh_seconds")
	check(c.BackoffSeconds <= c.MaxBackoffSeconds, "backoff_seconds must not exceed max_backoff_seconds")
	check(c.PublicationSeconds < c.RotationSeconds, "publication_seconds must be below rotation_seconds")
	check(c.KeyOverlapSeconds >= c.AssertionSeconds+c.ToleranceSeconds, "key_overlap_seconds must cover assertion_seconds + tolerance_seconds")
	check(c.RetentionSeconds >= c.StartSeconds+c.ToleranceSeconds, "retention_seconds must cover start_seconds + tolerance_seconds")
	check(c.RetentionSeconds >= c.RetryWindowSeconds+c.ToleranceSeconds, "retention_seconds must cover retry_window_seconds + tolerance_seconds")
	check(c.RetiredMonths >= 3 && c.RetiredMonths <= 120, "retired_months must be 3..120")
	check(c.DeletionWaitDays >= 7 && c.DeletionWaitDays <= 30, "deletion_wait_days must be 7..30")
	return errors.Join(es...)
}

// CleanupAt never schedules deletion of unresolved work. Boundaries are persisted,
// so later configuration changes cannot shorten an existing record's protection.
func CleanupAt(resolved, startDeadline, retryDeadline time.Time, retention int64) (time.Time, error) {
	if resolved.IsZero() {
		return time.Time{}, errors.New("unresolved outcome: cleanup prohibited")
	}
	if retention <= 0 || retention > 315360000 {
		return time.Time{}, errors.New("invalid retention_seconds")
	}
	t := resolved.Add(time.Duration(retention) * time.Second)
	for _, x := range []time.Time{startDeadline, retryDeadline} {
		if x.After(t) {
			t = x
		}
	}
	return t, nil
}
