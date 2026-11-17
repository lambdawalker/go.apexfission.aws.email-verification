# Key lifecycle, revocation and retention

[Design overview](../../verification-only-plan.md)

### Rotation lifecycle

KMS automatic rotation does not cover asymmetric signing keys. Our deployment or
rotation workflow will orchestrate replacement keys:

1. Create a new asymmetric KMS key with a new key identifier.
2. Publish its public JWK alongside the existing public key.
3. Publish seven days before cutover and confirm publication readiness; consumers
   own refresh and no consumer acknowledgment is required.
4. Stop signing with the old key, retaining its public key until all assertions
   signed with it expire, including allowed clock tolerance.
5. Retire the old key under a defined retention policy.

Account for consumer/CDN cache lifetimes when scheduling the overlap. Each
assertion's `kid` must identify the actual signing key, not a mutable alias.
Compromise response is separate from routine rotation: consumers must be able to
reject a revoked key even before its assertions expire. Disabling a KMS key alone
does not revoke signatures validated with a downloaded public key.

### Emergency revocation distribution and recovery

Decision: maintain one authoritative key-status document in S3, separate from
the immutable public-key archive. It lists allowed keys and permanent revoked
key IDs scoped to the issuer, including revocation time, reason and a monotonically
increasing status revision. Keep version history for audit. Never reuse a revoked
key ID. Historical signed archive records cannot override current status.

Direct AWS consumers read the status document through the AWS SDK from a
deployment-configured trusted location; HTTPS consumers read the same status via
a companion endpoint alongside JWKS. A standard JWKS alone does not convey
revocation history. Both adapters apply the selected 300-second refresh age,
30-second refresh cooldown and 900-second maximum age. A known revocation
overrides cached authorization immediately; unknown status is not authorization.
Validate status and key-set consistency before acceptance, rejecting ambiguous
or inconsistent views. Source authentication, freshness and rollback protection
must not depend solely on the possibly compromised assertion signing key.
Specify the wire schema, endpoint path and revision/freshness validation details
during implementation; preserve previously learned revocations.

A separate administrative role controls trust-status publication and emergency
replacement. The verifier runtime cannot modify the allowed/revoked list.
Authenticate recovery through independently trusted AWS administrative access.
A compromised key's signature cannot by itself approve its replacement, even
though ordinary archived key records are signed with the current assertion key.

Emergency procedure:
1. Stop use of the affected signing key, contain compromised credentials and
   permissions, and raise the operational alert.
2. Publish permanent revocation through the authoritative status source and both
   supported retrieval routes. Do not wait for outstanding assertions to expire.
3. Establish and administratively authorize a clean replacement key after
   containing the compromise; verify publication and resume signing. Emergency
   replacement bypasses the routine seven-day publication lead time.
4. Preserve audit history and assess affected assertions, key records and host
   actions. If no trustworthy replacement is ready, signing remains unavailable.

Consumers reject every newly presented assertion from a known revoked key,
including unexpired assertions claiming an issuance time before revocation.
An attacker with signing access can forge issuance timestamps. Distribution is
not instantaneous: the cache policy permits stale trust information until its
maximum age during outages. Failed refresh never renews that age, and a revoked
key never receives a stale-cache allowance.

#### Host-owned review of previously accepted proofs

At atomic acceptance, the host durably records issuer, signing key ID, assertion
ID, transaction/operation linkage, host acceptance time, and the rights or other
effects derived from that proof. Index or otherwise support lookup by issuer and
key ID. Do not rely solely on a token's claimed issuance time to bound exposure.

When the host learns of revocation, it rejects new assertions from that key and
starts a durable, idempotent, retryable review of affected accepted proofs and
their derived grants. The host determines which rights or sessions to suspend or
revoke, and which actions require investigation or compensating work. Revocation
does not automatically reverse irreversible actions or revoke unrelated rights
supported by independent evidence. Preserve progress, audit outcomes and alert
on remediation failures. Account for acceptance/revocation races so affected
records are eventually included.

This review belongs to the host, which owns the accounts, permissions and
acceptance records. No new remediation Lambda is required in the verifier; the
host may use its existing background-work mechanism. Previously accepted work
is subject to this incident policy, rather than blindly continuing a grant after
the host knows its supporting key was revoked.

### Retired KMS-key retention

Decision: retain a retired signing key in KMS for three calendar months after
the end of its authorized signing period (or earlier emergency retirement).
This is a deployment-controlled retention setting, distinct from the one-year
signing period, assertion acceptance overlap and indefinite public-key archive.
Use UTC calendar-month arithmetic, clamping to the last day of the destination
month when needed.

Stop new signing at retirement. For routine rotation, preserve public-key
retrieval through the required assertion overlap, with signing permission removed;
then disable the KMS key. Confirm the public key and signed historical record
have been archived before disabling/deleting the resource. On compromise, disable
affected signing immediately; the ordinary overlap does not justify continued
acceptance of revoked keys.

After the three-month retention period, schedule KMS deletion. AWS requires an
additional 7–30-day deletion waiting period (default 30 days), so this policy
means three months before scheduling, not guaranteed physical deletion exactly
at three months. Configure that waiting period within AWS's allowed range and
record the actual scheduled deletion time. Retention never permits automatic
reactivation of a retired or revoked signing key.

Retain the immutable S3 public-key record, signature and lifecycle/revocation
metadata indefinitely after KMS deletion. No private key is exported to S3.
Historical storage never restores active trust.

Reference: [AWS KMS key deletion](https://docs.aws.amazon.com/kms/latest/developerguide/deleting-keys.html).

### Decisions still open for this item

- Initial trust provisioning and detailed publication/recovery contracts; the signed-record format and S3 legal-hold protection are decided.
- Key-status wire schema, companion endpoint path, and concrete freshness/rollback checks.

References: [JWT (RFC 7519)](https://www.rfc-editor.org/rfc/rfc7519.html),
[JWT validation guidance (RFC 8725)](https://www.rfc-editor.org/rfc/rfc8725.html),
[KMS public keys](https://docs.aws.amazon.com/kms/latest/APIReference/API_GetPublicKey.html),
and [KMS rotation](https://docs.aws.amazon.com/kms/latest/developerguide/rotate-keys.html).

