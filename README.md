# jmap2nats

A small Go service that subscribes to a JMAP email server's push stream
and republishes newly-arrived emails onto a NATS JetStream stream.

- **Real-time** via JMAP's `EventSource` (server-sent events). No polling.
- **HA-safe**: run multiple replicas — JetStream message-id deduplication
  ensures each email lands on the stream once.
- **Outage-tolerant**: on boot the service re-checks the last N most-recent
  emails; anything still in the dedup window is dropped server-side as a
  duplicate, anything new is delivered. Messages are missed only if more
  than N arrived during downtime (default N = 100).
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

`jmap2nats -print-config` dumps the merged (defaults + your overrides)
config and exits — useful as a starting template. Pass `-verbose` to enable
debug-level logging.

Example `jmap2nats.json`:

```json
{
  "jmap": {
    "session_url": "https://api.fastmail.com/jmap/session",
    "token": "fmu1-yourtokenhere"
  },
  "nats": {
    "url": "nats://localhost:4222"
  },
  "stream": {
    "name": "JMAP_EMAILS",
    "subject_prefix": "jmap.email",
    "retention": "168h",
    "max_bytes": "64MiB",
    "dedup_window": "24h"
  },
  "parts": {
    "bucket": "email-parts",
    "max_bytes": "960MiB",
    "max_per_part": "25MiB"
  },
  "backfill_limit": 100
}
```

| JSON path               | Default                 | Notes                                              |
| ----------------------- | ----------------------- | -------------------------------------------------- |
| `jmap.session_url`      | (required)              | e.g. `https://api.fastmail.com/jmap/session`       |
| `jmap.token`            | (required)              | Bearer token. Keep this file mode 0600.            |
| `jmap.account_id`       | primary mail account    | Override if not the session primary.               |
| `nats.url`              | `nats://localhost:4222` |                                                    |
| `nats.creds`            | unset                   | Path to a NATS creds file (NGS etc).               |
| `stream.name`           | `JMAP_EMAILS`           |                                                    |
| `stream.subject_prefix` | `jmap.email`            | Subject = `<prefix>.<accountId>.<emailId>`.        |
| `stream.retention`      | `168h` (1 week)         | Stream `MaxAge`; also TTL on the object store.     |
| `stream.max_bytes`      | `64MiB`                 | Sizes accept `KiB`/`MiB`/`GiB`.                    |
| `stream.dedup_window`   | `24h`                   | Server-side `Nats-Msg-Id` dedup window.            |
| `parts.bucket`          | `email-parts`           | Object Store bucket for all body/attachment parts. |
| `parts.max_bytes`       | `960MiB`                | Bucket cap.                                        |
| `parts.max_per_part`    | `25MiB`                 | Skip individual parts larger than this.            |
| `backfill_limit`        | `100`                   | N most-recent emails to re-check on boot.          |

Total default storage footprint ≈ 1 GiB (64 MiB stream + 960 MiB parts).

## Running

```sh
jmap2nats -config ./jmap2nats.json
```

The service:

1. Authenticates with the JMAP server.
2. Creates or updates the JetStream stream and object-store bucket.
3. Re-checks the last N most-recent emails (boot recovery).
4. Opens a JMAP `EventSource` SSE connection and republishes every newly
   created email as it arrives.

For HA, run several copies pointed at the same NATS cluster. Duplicate
publishes (same JMAP email id) are dropped by the stream's dedup window.

## Data model

Each email becomes one NATS message:

- **Subject**: `<prefix>.<accountId>.<emailId>` (defaults to
  `jmap.email.<accountId>.<emailId>`).
- **`Nats-Msg-Id` header**: the JMAP email id, used for dedup.
- **Other headers** (filterable without parsing the body):
  - `Jmap-Account-Id`, `Jmap-Email-Id`, `Jmap-Thread-Id`
  - `Jmap-From`, `Jmap-To`, `Jmap-Cc`
  - `Jmap-Subject`
  - `Jmap-Received-At`, `Jmap-Sent-At` (RFC 3339)
  - `Jmap-Message-Id`, `Jmap-In-Reply-To`, `Jmap-References`
  - `Jmap-Mailbox-Ids`, `Jmap-Keywords`
  - `Jmap-Has-Attachment`, `Jmap-Size`
- **Body**: the JMAP `Email` object as JSON — same field names as
  [RFC 8621 §4][rfc8621-4] (`id`, `blobId`, `threadId`, `mailboxIds`,
  `keywords`, `from`, `to`, `subject`, `receivedAt`, `bodyStructure`,
  `textBody`, `htmlBody`, `attachments`, …). The `bodyValues` map is
  omitted — those bytes are in the object store. Each part with a
  `blobId` carries an extra `objectKey` field pointing into the bucket.

Every JMAP `EmailBodyPart` with a `blobId` — text bodies, html bodies,
inline images, attachments — is stored as one object in the
`email-parts` bucket, keyed `<emailId>/<blobId>`. Parts over
`parts.max_per_part` are skipped and flagged with `"skipped": true,
"objectKey": null` in the JSON; consumers can still fetch them
directly from the JMAP server via the `blobId`.

[rfc8621-4]: https://datatracker.ietf.org/doc/html/rfc8621#section-4

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

# Fetch any body part or attachment by its JMAP blobId
nats object get email-parts <EMAIL_ID>/<BLOB_ID> --output ./part.bin

# List stored parts
nats object ls email-parts

# Bucket info & storage usage
nats object info email-parts
```

## Limitations

- Only `created` events are published. JMAP `Email/changes` `updated`
  and `destroyed` ids are ignored — this is a mail forwarder, not a
  general state replicator.
- The boot-time backfill is bounded by `backfill_limit`. If a replica is
  offline for long enough that more than `backfill_limit` emails arrive
  before it returns, the oldest of those are not re-checked.
- One JMAP account per process. To bridge several accounts, run several
  instances with distinct configs (and ideally distinct subject prefixes
  if they share a stream).
