# Self-Update Specification

**Implemented.**

`colt update` upgrades the running Colt binary to the latest published release from the official release channel.

``` text
colt update             # check + apply the latest release
colt update --check     # report current vs latest without changing anything
```

The update source is the GitHub Releases channel already used by [`install.sh`](../../install.sh): assets are `colt-<os>-<arch>` plus a `checksums.txt` of SHA-256 digests. A self-update must not bypass the release pipeline's checksums.

| ID                  | Requirement                                                                                                                                                                                                                                                            | Acceptance specification                            |
|:--------------------|:-----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|:----------------------------------------------------|
| `UPDATE-CHECK-001`  | `colt update --check` **MUST** report the running version and the latest available release version without modifying the filesystem or the running binary.                                                                                                            | [`self_update.feature`](../../features/self_update.feature) |
| `UPDATE-APPLY-001`  | `colt update` **MUST** download and replace the running binary with the latest release. The new binary **MUST** report the new version, and replacement **MUST** be atomic and platform-safe (never wedge a running executable).                                       | [`self_update.feature`](../../features/self_update.feature) |
| `UPDATE-SAFETY-001` | Update **MUST** verify the downloaded artifact against the published SHA-256 digest before replacing it, **MUST** leave the original binary intact on failure or interruption, and **MUST NOT** expose credentials or provider response bodies in output, logs, or errors. | [`self_update.feature`](../../features/self_update.feature) |

The Git credential-helper integration keeps `colt` at a stable path; a self-update must preserve that path and its executable permissions so existing credential-helper configuration keeps working. Verify-then-replace, not replace-then-verify.