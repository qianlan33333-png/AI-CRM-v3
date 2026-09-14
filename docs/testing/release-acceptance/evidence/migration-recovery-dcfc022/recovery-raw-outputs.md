# Recovery raw-output archive

`recovery-raw-outputs.tar` retains the six original source- and restore-
database signature outputs from the 2026-09-14 isolated migration recovery
exercise. The paired files are intentionally byte-identical only after the
separate database commands were compared; keeping both member paths preserves
that two-database evidence without adding six duplicate paths to the source
tree.

The archive and every member are pinned in
`recovery-raw-outputs.manifest.json`. It contains exactly the six listed regular
files, written with normalized tar metadata (mtime, uid, and gid are zero); no
AppleDouble or extended-attribute members are included. To inspect or extract them from this
directory:

```sh
shasum -a 256 recovery-raw-outputs.tar
tar -tf recovery-raw-outputs.tar
mkdir recovery-raw-outputs
tar -xf recovery-raw-outputs.tar -C recovery-raw-outputs
```

Compare each extracted member against the manifest `bytes` and `sha256` value.
The original expanded copies remain in the separate external acceptance-evidence
workspace; this archive is the tracked release-candidate representation.
