# Assertions and acceptance timing

[Design overview](../verification-only-plan.md)

## Assertion schema, signing, and trusted keys

Status: initial design baseline from our discussion; not implemented. Exact field
names below are proposed contract names. ES256 and operational timing defaults are selected for the first version.

### Format and initial schema

Use a digitally signed JWT in JWS compact serialization. JWT defines the claims;
JWS protects the header and payload with a signature. This is a verification
assertion, not a session/access token, OAuth flow, or OIDC ID token.

All payload fields below are required in the initial profile.

| Payload field | JSON type | Meaning and validation |
| --- | --- | --- |
| `schema_version` | integer | Required value `1` for this profile; reject missing or unsupported versions. |
| `iss` | string | Configured verifier issuer; must exactly match the trusted issuer. |
| `aud` | string | Intended host identifier; require one exact configured audience in this profile. |
| `email` | string | Address verified for this transaction; must match the address bound at initiation. |
| `email_verified` | boolean | Must be `true`; unsuccessful attempts do not produce success assertions. |
| `nonce` | string | Host-generated random request binding, echoed unchanged from authorized initiation. |
| `transaction_id` | string | Verifier transaction identifier, matched against the pending operation. |
| `verified_at` | integer | UTC Unix seconds when the required proofs were successfully validated. |
| `iat` | integer | UTC Unix seconds when this assertion was issued. |
| `exp` | integer | UTC Unix seconds at which acceptance expires. |
| `jti` | string | Unique assertion identifier, retained across delivery retries for deduplication. |

The host nonce, transaction ID, and assertion ID each use independently generated
32-byte cryptographically random values encoded as canonical unpadded base64url
(43 characters). See the encoding and evolution rules below.
Operation names, account/session associations, and eligibility stay in host storage.
Do not include A, B, C, their secret values, or session credentials in the assertion.

| Protected header | Initial rule |
| --- | --- |
| `typ` | Require the exact value `email-verification+jwt` in the protected header. This is our selected local profile type, not a claim of a registered media type. |
| `alg` | Require `ES256` for the first version. Reject `none` and every other algorithm; bind each trusted key to its configured algorithm. |
| `kid` | Stable unique identifier for the specific signing key in the trusted key set; never reuse it for a replacement key. |

The assertion type is finalized as `email-verification+jwt` in the protected
JWT/JWS header's `typ` field, not in the payload. Reject a missing or different
header type even if the signature is valid; a payload `typ` cannot substitute
for it. This identifies evidence of completed verification, not an action token
used to initiate verification. Dedicated validators must also reject this
assertion wherever a login or access token is required. The type check supplements,
and never replaces, signature and claim validation. This follows the explicit
typing pattern in RFC 8725 without claiming interoperability with other profiles.

Validate types, required fields, signature, issuer, audience, request bindings,
and time bounds. Require `verified_at <= iat < exp`, and enforce a configured
maximum verification age and assertion lifetime, with a bounded clock tolerance.
The configured defaults are a ten-minute assertion lifetime, fifteen-minute
maximum verification age, and three-second clock tolerance, as specified below.
The email-proof lifetime is a separate ten-minute window. Reject duplicate JSON members
and unsupported assertion types. Use dedicated validation rules so login tokens
and verification assertions cannot substitute for one another.

Prefer redelivery of the same persisted signed assertion. Retries must preserve
the verification identity, `jti`, bindings, verification time, and original
expiration; retrying must not renew the acceptance window. The host still
atomically consumes its pending operation and records durable follow-up work.
A signature provides integrity, not confidentiality or automatic single use.

### Configurable timing and host acceptance

These are selected system configuration defaults, not hard-coded constants or
durations mandated by a standard. Configuration is deployment-controlled, never
chosen by an untrusted client or by a token's claims.

| Configuration | Default | Clock starts at | Purpose |
| --- | --- | --- | --- |
| `assertion_lifetime_seconds` | 600 (10 minutes) | `iat`, original assertion issuance | Limits how long an issued assertion can be accepted. |
| `max_verification_age_seconds` | 900 (15 minutes) | `verified_at`, successful proof validation | Prevents delayed issuance from making old verification evidence fresh. |
| `clock_tolerance_seconds` | 3 seconds | Applied during timestamp validation | Allows small differences between verifier and host clocks. |

The separate email-proof lifetime remains ten minutes from proof creation. It
limits when the user can complete verification, not when the host accepts the
subsequently issued assertion.

The verifier sets `exp = iat + assertion_lifetime_seconds` at original issuance.
The host enforces its trusted configured maximum assertion lifetime as well as
the signed expiration and maximum verification age. Configuration must specify
positive integer lifetimes and a nonnegative integer clock tolerance, with
checked timestamp arithmetic. Deploy issuer and consumer settings coherently;
a host may enforce stricter policy. Increasing configuration never rewrites or
extends an existing signed expiration.

With `now` sampled at host acceptance and `S` the configured tolerance, require:

```text
verified_at <= iat < exp
exp - iat <= host_config.assertion_lifetime_seconds
iat <= now + S
verified_at <= now + S
now < exp + S
now < verified_at + host_config.max_verification_age_seconds + S
now < host_pending_operation_deadline
```

The host-owned operation deadline receives no additional tolerance in this
profile. Apply tolerance once to each relevant comparison, not cumulatively.
It does not change signed timestamps or excuse invalid bindings, signatures,
revoked keys, or previously consumed operations. Keep clocks synchronized;
tolerance is not an allowance for arbitrary processing delays.

Both post-verification limits run in parallel. The effective acceptance deadline
is the earliest of signed expiration plus tolerance, verification time plus
maximum age plus tolerance, and the host's own operation deadline.

#### Examples with the default configuration

Assume verification succeeds at 12:00:00, all other checks pass, the operation
is still pending, and its host deadline is later than the shown times.

| Scenario | Assertion issuance | Signed expiration | Verification-age deadline before tolerance | Host acceptance |
| --- | --- | --- | --- | --- |
| Immediate issuance | 12:00:00 | 12:10:00 | 12:15:00 | Accept at 12:09:00; time checks permit 12:10:02; reject at 12:10:03. |
| Issuance delayed seven minutes | 12:07:00 | 12:17:00 | 12:15:00 | Accept at 12:14:00; time checks permit 12:15:02; reject at 12:15:03 even though the JWT has not reached `exp`. |

If the host's pending-operation deadline is 12:08:00, both examples must instead
reject at or after 12:08:00. Delivery retries retain the original assertion,
identifiers, timestamps, and expiration. They never restart either clock.
Once the host has atomically accepted and recorded durable work, that work may
finish after expiration; retries of the work do not constitute a fresh acceptance.
If no acceptance occurred before the effective deadline, require new verification.

Test both deadlines independently, future timestamps, exact tolerance boundaries,
shorter host deadlines, configuration changes, and retries without renewal.

### ID encoding and email comparison

Decisions for version 1:

- Generate `nonce` on the host and `transaction_id` and `jti` on the verifier
  independently using a cryptographically secure random generator. Each is
  32 bytes encoded as canonical unpadded base64url (43 characters).
- Validate the alphabet, length, decoded byte length, and canonical encoding
  (decode then re-encode must produce the same string). Reject padding,
  whitespace, and alternative encodings. Compare identifiers exactly and
  case-sensitively; never normalize or derive one identifier from another.
- Preserve the same identifiers on retries of the same operation/result.
  A new verification transaction receives new identifiers. These IDs bind and
  identify records; possession of an ID alone never authorizes an operation.
  The signing key's `kid` is a separate deployment-managed identifier and is
  not subject to this random request-ID encoding.
- Bind the exact email string accepted through authorized initiation, use that
  address for delivery, and return it unchanged in the signed assertion.
  The host compares the decoded JSON string exactly against the stored pending
  operation's email, not against a fresh client-supplied value or raw JSON bytes.
- Do not trim, case-fold, remove dots, remove plus tags, or apply Unicode
  normalization during assertion comparison. Any supported validation or
  normalization policy runs before request binding; the host must store and
  send the same resulting value. Unsupported address inputs are rejected,
  rather than silently rewritten by the verifier.
- This equality rule binds one transaction. It does not define account uniqueness
  or claim that differently written addresses necessarily identify different
  mailboxes; those account policies remain host-owned.

### Schema evolution

Require a signed payload field `schema_version` with integer value `1`.
The protected header type remains `email-verification+jwt`; schema version
identifies the contract revision, while the type identifies the message purpose.

Within version 1, optional informational payload fields may be added only when
ignoring them preserves the existing security meaning. Consumers may ignore
unknown informational payload fields, but must still validate every required
field and reject duplicate JSON members. This does not relax JOSE header
processing or allow unsupported critical header parameters.

Changes to required fields, types, interpretation, or security-relevant rules
require a new schema version. Reject missing, malformed, or unsupported versions;
never silently interpret a newer version as version 1. Deploy explicit support
to consumers before issuing a new version, and bound any period accepting both
versions by policy. Do not turn a security-critical requirement into an optional
field older consumers can ignore.

Acceptance checks must include malformed/noncanonical IDs, mismatched email
strings, missing/unsupported versions, and harmless informational additions that
leave all required checks intact.

