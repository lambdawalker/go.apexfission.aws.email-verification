package verification

import (
	"context"
	"errors"
	"github.com/lambdawalker/go.apexfission.aws.email-verification/storage"
	"slices"
)

type TrustState struct {
	Revision    uint64
	Digest      string
	Revocations []string
}

// CommitTrust stores a validated authoritative view. Callers validate schema,
// issuer, keys and authenticated source first; a successful commit alone is not
// a freshness proof. Digest is SHA-256 of the exact authoritative document bytes.
func CommitTrust(ctx context.Context, s storage.Store, issuer, source string, next TrustState) error {
	if issuer == "" || source == "" || next.Revision == 0 || !validID(next.Digest) {
		return ErrInvalid
	}
	k := "trust:" + key(issuer, source)
	for i := 0; i < 8; i++ {
		old, v, e := read[TrustState](ctx, s, k)
		if e != nil && !errors.Is(e, storage.ErrMissing) {
			return e
		}
		if next.Revision < old.Revision {
			return ErrInvalid
		}
		if next.Revision == old.Revision {
			if next.Digest != old.Digest {
				return ErrInvalid
			}
			return nil
		}
		for _, revoked := range old.Revocations {
			if !slices.Contains(next.Revocations, revoked) {
				return ErrInvalid
			}
		}
		e = write(ctx, s, k, v, next)
		if errors.Is(e, storage.ErrConflict) {
			continue
		}
		return e
	}
	return storage.ErrConflict
}
