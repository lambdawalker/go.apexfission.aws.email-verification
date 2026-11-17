# Public-key archive and signed records

[Design overview](../../verification-only-plan.md)

### First-draft public-key archive and signed records

Decision: retain historical public keys and their metadata indefinitely in S3,
using S3 Object Lock legal holds on version-specific archive records. Enable
Object Lock and Versioning on the archive bucket. No timed Governance or
Compliance retention is required for this first draft.

- Apply legal hold ON when uploading each signed record and verify protection
  before advertising the record as published. Failed protection checks fail
  publication and emit an operational alert.
- The publishing role may create protected records but must not remove holds;
  enforce ON-only hold-setting permissions. The verifier runtime has no archive
  mutation permissions. Neither role may delete archive versions.
- Reserve hold removal for a separate administrative role, with audited,
  exceptional use. Routine retirement, revocation and KMS deletion never remove
  holds or delete public history.
- Record and retrieve the exact bucket, object key and S3 version ID. Object
  Lock protects that version; it does not prevent a newer version under the same
  name or a delete marker. Do not resolve historical records by latest version.
- Keep the authoritative current key-status document separately updateable.
  Revocation changes current trust without modifying historical records.
- Legal holds have no expiration but can be removed by an authorized
  administrator. This is indefinite protection with an administrative escape,
  not irreversible retention. Signatures and trust-status validation remain
  necessary; storage protection does not establish authenticity.

Reference: [S3 Object Lock](https://docs.aws.amazon.com/AmazonS3/latest/userguide/object-lock.html).
This is a recorded design decision, not an AWS deployment.

#### Signed public-key record format

Decision: store one JWS Compact Serialization record per published public key,
with a UTF-8 JSON payload and an ES256 signature made by the current authorized
assertion-signing KMS key. Store the complete compact JWS under S3 legal hold.
This is a key record, not an email-verification assertion.

| Part | Required field | Meaning |
| --- | --- | --- |
| Protected header | `typ` | Exact local profile value `email-verification-key-record+jws`; not a claim of a registered media type. |
| Protected header | `alg` | Exact value `ES256`. |
| Protected header | `kid` | Identifier of the existing key signing this record. |
| Payload | `schema_version` | Integer `1`; reject unsupported versions. |
| Payload | `issuer` | Expected verifier issuer identifier. |
| Payload | `record_id` | Unique identifier for this historical record. |
| Payload | `public_key` | Public JWK for the key being published, with its own `kid`, `kty=EC`, `crv=P-256`, `alg=ES256`, `use=sig`, and public coordinates `x` and `y`. Never include private key material such as `d`. |
| Payload | `kms_key_arn` | Immutable ARN of the published key, never a mutable alias. |
| Payload | `created_at` | Key creation time. |
| Payload | `recorded_at` | Record issuance time. |
| Payload | `planned_activation_at` | Intended signing activation time when this record was published. |

Use UTC RFC 3339 timestamps for the three record time fields. The protected
header's `kid` identifies the signer; `public_key.kid` identifies the key being
introduced. For routine rotation, key A signs the record describing key B before
cutover.

Preserve the exact compact JWS bytes. Verify its original encoded header and
payload using the standard JWS signing input; never parse and reserialize JSON
to reconstruct the signed bytes. No custom JSON canonicalization is required.
Reject duplicate JSON member names, unexpected algorithms, unsupported schemas,
and record/assertion type confusion. Resolve the signer from established trusted
configuration, not an embedded key or a token-supplied URL.

The historical record has no expiration field. Its continued storage or
mathematically valid signature does not authorize the published key forever.
The separately updateable key-status document controls actual activation,
retirement and revocation. `planned_activation_at` preserves the original plan;
it does not override current status. Preserve record identity across publication
retries rather than silently replacing an archived record.

The S3 version ID is recorded after upload in publication metadata; it is not
included in the pre-upload signed payload. Consumers retrieve the exact archived
version as required above.

References: [JWS, RFC 7515](https://www.rfc-editor.org/rfc/rfc7515.html)
and [JWK, RFC 7517](https://www.rfc-editor.org/rfc/rfc7517.html).

This first draft deliberately does not introduce a separate key-management signing
key. Consumers must already trust the record signer through trusted configuration
or an authenticated established trust path. A signer public key supplied only
inside the same record cannot establish authenticity. Initial trust provisioning
must be documented; self-signing the initial record alone does not establish trust.

An immutable archive records historical facts, not perpetual authorization.
Maintain current acceptance/retirement/revocation state separately through the
authoritative trust source. Archive records never override that state. A valid
record signature does not prove that a key is currently authorized or that a
downloaded trust view is fresh; enforce the freshness and revocation policies.
For routine retirement, retain active verification access through the last
old-key assertion expiration plus configured clock tolerance, then stop accepting
that key. Retain its archived public record afterward. Revocation due to compromise
takes precedence over ordinary overlap. Retired private KMS-key retention/deletion
follows the three-month policy below.

#### Explicitly accepted first-draft risks and limits

- Compromise of the current private signing key, or unauthorized use of its KMS
  signing permission, can produce forged assertions and convincingly signed fake
  replacement-key records. Signing records with that same key does not contain
  this compromise or make recovery through that key trustworthy.
- An attacker who can publish a forged record and influence the trusted key
  mapping may extend the compromise into future keys. Storage access restrictions
  and signer authorization remain essential; the record signature is additional
  evidence, not a replacement for those controls.
- Legitimately signed old records can be replayed. Historical signature validity
  must never re-enable a retired/revoked key or reset trust-information freshness.
- Emergency recovery requires an independently authenticated administrative
  update of trust configuration; do not rely solely on authorization signed by
  the compromised key. Use the administrative recovery and distribution procedure below.
- Immutability preserves what was written, including any malicious record written
  by an authorized attacker. It does not prove the original record was legitimate.
- A separate key-management authority remains a possible future enhancement,
  not a first-draft requirement.

