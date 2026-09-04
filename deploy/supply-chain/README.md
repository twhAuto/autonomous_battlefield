# Supply-chain trust root

Jenkins publishes private GHCR images only after the repository gates pass. Set
these credentials before enabling publication:

- `battlefield-ghcr`: username and classic PAT restricted to
  `read:packages,write:packages`.
- `battlefield-cosign-key`: encrypted Cosign private-key file.
- `battlefield-cosign-password`: the private-key password.
- `battlefield-cosign-public-key`: the corresponding reviewed public-key file.

Replace `cosign.pub.example` with a reviewed `cosign.pub` only after the private
key has been stored in Jenkins. Never commit `cosign.key`.

The CI signing stage publishes Cosign v3 OCI 1.1 referrers and a legacy
digest-tag mirror for every signature and signed attestation. OCI referrers are
the source copied during registry promotion. The mirror allows the pinned
Kyverno 1.15 admission controller to discover the same evidence in private
GHCR; it is not a separate trust root or a mutable deployment artifact.

Rotate the key by pausing publication, installing a new credential pair,
committing the new public key, signing a canary digest, verifying it, and then
resuming. Keep the retired public key until every deployable and rollback digest
signed by it has expired from retention. If compromise is suspected, disable
the credential immediately, block deployment, preserve audit evidence, rotate,
and re-sign only artifacts rebuilt from reviewed source.
