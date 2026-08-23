# Archive Format

Marina writes its own archive format directly to S3 -- there is no external backup tool involved,
and no proprietary container format either. This document is the spec: everything here is enough
to read a Marina backup back without Marina, using nothing but `age`, `zstd`, and `tar`.

## Object layout

Each backup run ("snapshot") writes one object per target plus a manifest, all under a shared
timestamp prefix:

```
<prefix>/<instanceId>/<timestamp>/volume-<name>.tar.zst[.age]
<prefix>/<instanceId>/<timestamp>/db-<name>.tar.zst[.age]
<prefix>/<instanceId>/<timestamp>/manifest.json[.age]
```

- `<prefix>` is the instance's optional `s3.prefix` config, or omitted entirely if unset.
- `<timestamp>` is `20060102-150405` (the instance's backup run start time), shared by every
  object in that run.
- Each target (one Docker volume, or one database dump) becomes its own `<type>-<name>.tar.zst`
  object -- a plain tar archive of the staged directory tree, compressed with
  [zstd](https://github.com/facebook/zstd).
- The `.age` suffix is present only when the instance has `encryption.password` configured. When
  it's absent, every object in the run is stored as plaintext tar+zstd.

## The manifest is the completion marker

`manifest.json` is written **last**, after every artifact has finished uploading. Its presence is
what marks a snapshot complete:

- `Backend.ApplyRetention` only considers timestamp prefixes that have a manifest -- a run that
  was interrupted partway through (crash, network failure, container killed) leaves orphaned
  artifact objects with no manifest, and retention leaves them alone rather than risking a
  half-deleted incomplete run.
- `marinactl list` only shows timestamps with a manifest, for the same reason.

Schema:

```json
{
  "instanceId": "hetzner-s3",
  "node": "prod-1",
  "timestamp": "20260823-020000",
  "marinaVersion": "0.11.0",
  "encrypted": true,
  "compression": "zstd",
  "artifacts": [
    {
      "key": "volume-app-uploads.tar.zst.age",
      "type": "volume",
      "name": "app-uploads",
      "sizeBytes": 918273,
      "sha256": "b5b2c1a3..."
    }
  ]
}
```

`sha256` is computed over the object exactly as it was uploaded -- i.e. over the encrypted bytes
when encryption is on, not the plaintext. `marinactl restore` verifies it while streaming, so a
truncated or corrupted object is caught immediately.

When encryption is enabled, the manifest itself is encrypted too (it names every volume and
database backed up, so leaving it in plaintext would defeat much of the point).

## Encryption

Encryption is [age](https://age-encryption.org/), password-based
(`age.NewScryptRecipient`/`NewScryptIdentity`). Marina doesn't invent any framing on top of it --
each `.age` object is exactly what the standalone `age` CLI produces and reads. age's own STREAM
construction handles chunking, authentication, and truncation detection; there is nothing
Marina-specific to know about the ciphertext layout.

## Retention

Retention keeps the newest snapshot in each of the most recent N days, M weeks, and K months
(independent buckets -- the snapshot kept for "today" and the one kept for "this week" can be the
same object or different ones), per the instance's `retention: "daily:weekly:monthly"` policy. The
single newest snapshot is always kept, even under an all-zero policy. See
`helpers.SelectExpiredSnapshots` for the exact algorithm.

## Recovering a backup without Marina

Because the format is just tar+zstd, optionally wrapped in age, recovery only needs three
widely-available tools:

```bash
# Encrypted archive, password-based:
age -d backup.tar.zst.age | zstd -d | tar -x

# Encrypted archive, with an age identity file:
age -d -i key.txt backup.tar.zst.age | zstd -d | tar -x

# Unencrypted archive:
zstd -d < backup.tar.zst | tar -x
```

For everyday use, [`marinactl`](../README.md#restoring-a-backup) does this for you -- it also
reads the manifest so you can restore by snapshot timestamp or target name instead of guessing
object keys, and it verifies the SHA-256 checksum as it streams.
