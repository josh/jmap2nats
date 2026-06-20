# jmap2nats

A small Go service that subscribes to a JMAP email server's push stream
and republishes newly-arrived emails onto a NATS JetStream stream.

- **Real-time** via JMAP's `EventSource` (server-sent events). No polling.
- **HA-safe**: run multiple replicas — every publish carries a `Nats-Msg-Id`
  set to `<accountId>/<emailId>`, so concurrent publishes from different
  replicas are deduplicated server-side within the stream's `DuplicateWindow`.
- **Outage-tolerant**: the service persists the last successfully processed
  JMAP `Email` state in a NATS KV bucket and resumes from that cursor after
  restarts. If the server can no longer calculate changes from the saved state,
  jmap2nats falls back to a bounded recent backfill.
- **JMAP-faithful** payload: the NATS message body is the JMAP `Email`
  object as JSON (RFC 8621 field names), with body/attachment bytes
  externalised to an Object Store and referenced by key.

Works with any JMAP-compatible mail server. [Fastmail][fastmail] is
straightforward to use; create an API token from the Fastmail account
settings.

[fastmail]: https://www.fastmail.com/

## Install

```sh
go install github.com/josh/jmap2nats@latest
```

Or clone and `go build .` in the working directory.

You'll need a reachable NATS server with JetStream enabled. The official
`nats-server` works out of the box; start it locally with
`nats-server -js`.

## Configuration

A single JSON file. Path resolution, in order:

1. `-config <path>` flag.
2. `JMAP2NATS_CONFIG` env var.
3. `./jmap2nats.json` in the working directory.

`jmap2nats -print-config` dumps a default template config and exits without
loading a config file. Use it as a starting point for `jmap2nats.json`. Pass
`-verbose` to enable debug-level logging.

Example `jmap2nats.json`:

```json
{
  "jmap": {
    "session_url": "https://api.fastmail.com/jmap/session",
    "token_file": "/etc/jmap2nats/token"
  },
  "nats": {
    "url": "nats://localhost:4222"
  },
  "stream": {
    "name": "JMAP_EMAILS",
    "subject_prefix": "jmap.email",
    "max_age": "168h",
    "max_bytes": "64MiB",
    "dedup_window": "24h"
  },
  "parts": {
    "bucket": "email-parts",
    "max_bytes": "960MiB",
    "max_per_part": "25MiB"
  },
  "cursor": {
    "bucket": "JMAP_EMAILS_CURSOR"
  },
  "backfill_limit": 100
}
```

| JSON path                   | Default                 | Notes                                                                                                                                       |
| --------------------------- | ----------------------- | ------------------------------------------------------------------------------------------------------------------------------------------- |
| `jmap.session_url`          | (required)              | e.g. `https://api.fastmail.com/jmap/session`                                                                                                |
| `jmap.token_file`           | (required)              | Path to a file containing the bearer token. Trailing whitespace is trimmed. Keep mode 0400.                                                 |
| `jmap.account_id`           | primary mail account    | Override if not the session primary.                                                                                                        |
| `nats.url`                  | `nats://localhost:4222` |                                                                                                                                             |
| `nats.token_file`           | unset                   | Path to a file containing a NATS auth token. Trailing whitespace is trimmed.                                                                |
| `nats.user`                 | unset                   | NATS username (literal); set together with `nats.password_file`. Mutually exclusive with `nats.user_file`.                                  |
| `nats.user_file`            | unset                   | Path to a file containing the NATS username; alternative to the literal `nats.user`. Trailing whitespace is trimmed.                        |
| `nats.password_file`        | unset                   | Path to a file containing the NATS password (plaintext, even when the server stores a bcrypt hash). Trailing whitespace is trimmed.         |
| `nats.creds_file`           | unset                   | Path to a NATS creds file (decentralized JWT / NGS); the `nsc`-generated bundle of the user JWT + signing nkey seed.                        |
| `nats.nkey_seed_file`       | unset                   | Path to a NATS nkey seed file. The seed signs the server's challenge.                                                                       |
| `stream.name`               | `JMAP_EMAILS`           |                                                                                                                                             |
| `stream.subject_prefix`     | `jmap.email`            | Subject = `<prefix>.<accountId>`.                                                                                                           |
| `stream.max_age`            | `168h` (1 week)         | Stream `MaxAge`; also TTL on the object store.                                                                                              |
| `stream.max_bytes`          | `64MiB`                 | Sizes accept `KiB`/`MiB`/`GiB`.                                                                                                             |
| `stream.dedup_window`       | `24h`                   | Server-side `Nats-Msg-Id` dedup window. Catches concurrent HA publishes; boot-time republishes are gated separately by the high-water mark. |
| `stream.externally_managed` | `false`                 | Skip create/update; only verify the stream exists. Set true when another operator (e.g. NACK) owns the stream config.                       |
| `parts.bucket`              | `email-parts`           | Object Store bucket for all body/attachment parts.                                                                                          |
| `parts.max_bytes`           | `960MiB`                | Bucket cap.                                                                                                                                 |
| `parts.max_per_part`        | `25MiB`                 | Skip individual parts larger than this.                                                                                                     |
| `cursor.bucket`             | `<stream.name>_CURSOR`  | JetStream KV bucket for durable per-account JMAP state cursors.                                                                             |
| `backfill_limit`            | `100`                   | N most-recent emails to re-check on first run or expired-state fallback.                                                                    |

NATS authentication is optional; configure at most one of `nats.token_file`,
`nats.user`/`nats.user_file` + `nats.password_file`, `nats.creds_file`, or
`nats.nkey_seed_file` (otherwise the connection is anonymous). Every secret is
referenced by file path — nothing is read from an environment variable or CLI
flag.

- **Token**: `nats.token_file`.
- **Username/password** (plaintext _and_ bcrypted): `nats.user` (or
  `nats.user_file` to read the username from a file) + `nats.password_file`.
  Bcrypt is a server-side storage concern — the client always sends the
  plaintext password, so the same config covers both.
- **Decentralized JWT**: `nats.creds_file`, the `nsc`-generated creds file
  (operator/account/user JWT + signing nkey).
- **NKEY**: `nats.nkey_seed_file`; the seed signs the server's challenge.

Total default storage footprint ≈ 1 GiB (64 MiB stream + 960 MiB parts).

## Running

```sh
jmap2nats -config ./jmap2nats.json
```

`jmap2nats -version` (or `jmap2nats version`) prints the build version
and exits.

The service:

1. Authenticates with the JMAP server.
2. Creates or updates the JetStream stream, object-store bucket, and cursor
   KV bucket.
3. Loads the persisted JMAP cursor for the account. If none exists, it
   bootstraps once with the bounded recent backfill and then saves the current
   JMAP state.
4. Opens a JMAP `EventSource` SSE connection and republishes every newly
   created email as it arrives.

For HA, run several copies pointed at the same NATS cluster. Concurrent
publishes (same JMAP account id and email id from different replicas) are
rejected by the stream's `Nats-Msg-Id` dedup window; replicas share the same
per-account cursor in NATS KV, so normal restarts resume from the last saved
JMAP state rather than from message ordering in the stream.

## Data model

Each email becomes one NATS message:

- **Subject**: `<prefix>.<accountId>` (defaults to `jmap.email.<accountId>`).
  All emails for an account share one subject; the emailId is carried in
  the `Nats-Msg-Id` and `Jmap-Email-Id` headers and in the JSON body's
  `id` field, not in the subject.
- **`Nats-Msg-Id` header**: `<accountId>/<emailId>`, used for dedup.
- **Schema version**: `Jmap2nats-Schema-Version: 1`. Schema version `1`
  covers the subject format, public message headers, JSON body shape, and
  object-store key format.
- **Other headers** (filterable without parsing the body):
  - `Jmap2nats-Schema-Version`
  - `Jmap-Account-Id`, `Jmap-Email-Id`, `Jmap-Thread-Id`
  - `Jmap-From`, `Jmap-To`, `Jmap-Cc`
  - `Jmap-Subject`
  - `Jmap-Received-At`, `Jmap-Sent-At` (RFC 3339)
  - `Jmap-Message-Id`, `Jmap-In-Reply-To`, `Jmap-References`
  - `Jmap-Mailbox-Ids`, `Jmap-Keywords`
  - `Jmap-Has-Attachment`, `Jmap-Size`

  Header values are single-line: any `\r`/`\n` in the source is collapsed to a
  space. `Jmap-From`, `Jmap-To`, and `Jmap-Cc` are best-effort
  human-readable (`Name <addr>`) for filtering and display only — display
  names are not quoted and may contain the `, ` list separator, so they are not
  safely splittable. Structured consumers should parse the JSON body's `from` /
  `to` / `cc` arrays instead.

- **Body**: the JMAP `Email` object as JSON -- same field names as
  [RFC 8621 §4][rfc8621-4] (`id`, `blobId`, `threadId`, `mailboxIds`,
  `keywords`, `from`, `to`, `subject`, `receivedAt`, `textBody`,
  `htmlBody`, `attachments`, ...). The `bodyValues` map is
  omitted -- those bytes are in the object store. Each part with a
  `blobId` reports one of the following outcomes:
  - Stored parts include an `objectKey` field pointing into the bucket.
  - Skipped parts include `"skipped": true` and omit `objectKey`.
  - Errored parts include `"error": "..."` and omit `objectKey`.

Every JMAP `EmailBodyPart` with a `blobId` -- text bodies, html bodies,
inline images, attachments -- is stored as one object in the
`email-parts` bucket, keyed `<accountId>/<emailId>/<blobId>`. This lets
multiple JMAP accounts safely share one stream and object-store bucket.

Consumers must use each part's `type` to interpret the object contents:

- `text/*` entries in `textBody` and `htmlBody` contain JMAP-decoded
  UTF-8 text from `Email/get bodyValues`, with line endings normalized
  to LF.
- Non-text body parts and attachments contain raw bytes from
  `Blob/download`.

If a required text body value is missing, truncated, or reports a JMAP
encoding problem, the part is flagged with `"error": "..."` and no
`objectKey`; jmap2nats does not fall back to raw blob bytes for those
text bodies. Parts over `parts.max_per_part` are skipped and flagged
with `"skipped": true` and no `objectKey`; consumers can still fetch
them directly from the JMAP server via the `blobId`.

[rfc8621-4]: https://datatracker.ietf.org/doc/html/rfc8621#section-4

## Stability

`Jmap2nats-Schema-Version: 1` is a frozen wire contract. Within schema `1`
these will not change incompatibly:

- **Subject** `<prefix>.<accountId>`, **dedup id** (`Nats-Msg-Id`)
  `<accountId>/<emailId>`, and **object-store key** `<accountId>/<emailId>/<blobId>`.
  These embed JMAP ids with raw `.` and `/` separators, which is unambiguous
  because RFC 8620 §1.2 restricts ids to `A-Za-z0-9_-`; jmap2nats rejects a
  server that violates this rather than writing a malformed key.
- The **public headers** and the **JSON body shape** (including the per-part
  `objectKey` / `skipped` / `error` outcomes). `hasAttachment` is always
  present in the body. `Jmap-Mailbox-Ids` and `Jmap-Keywords` are sorted, but
  remain semantically unordered sets.
- **Delivery is at-least-once.** Each email is published before its cursor is
  checkpointed, so a crash mid-batch re-publishes; the stream's `Nats-Msg-Id`
  dedup window (`stream.dedup_window`) absorbs the replay. Consumers should
  treat the dedup id as the idempotency key.

Some JetStream settings are fixed when a resource is first created and
**cannot be changed in place** afterward — plan them before your first deploy:

- **`replicas`** (one shared value for the stream, object store, and cursor KV).
  Default `1`; set it to e.g. `3` up front for HA. Raising it later requires
  deleting and recreating the buckets.
- **Storage** is always file-backed; **retention** is `limits` / `discard old`.

## Consuming with the `nats` CLI

```sh
# Watch new emails as they arrive
nats sub 'jmap.email.>'

# Stream info & storage usage
nats stream info JMAP_EMAILS

# Replay everything currently in the stream into a new consumer
nats consumer add JMAP_EMAILS replay --filter 'jmap.email.>' \
  --deliver=all --replay=instant --ack=none --pull
nats consumer next JMAP_EMAILS replay --count 5

# Fetch any body part or attachment by its objectKey
nats object get email-parts <ACCOUNT_ID>/<EMAIL_ID>/<BLOB_ID> --output ./part.bin

# List stored parts
nats object ls email-parts

# Bucket info & storage usage
nats object info email-parts
```

## Limitations

- Only `created` events are published. JMAP `Email/changes` `updated`
  and `destroyed` ids are ignored — this is a mail forwarder, not a
  general state replicator.
- Outage recovery is durable while the JMAP server can calculate changes from
  the saved cursor. First-run bootstrap and expired-state fallback are bounded
  by `backfill_limit` and ordered by `receivedAt`; if more than that many
  emails need fallback recovery, older creations are not re-checked.
- One JMAP account per process. To bridge several accounts, run several
  instances with distinct configs. Account-qualified dedup ids and object
  keys allow those instances to share one stream and parts bucket safely.
