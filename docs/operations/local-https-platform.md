# Local HTTPS and retained CA

[AB-142 — Capture and reproduce local HTTPS routes and CA trust](https://app.notion.com/p/3edb7a19f8f7810ebd5de5d75beaa267)
provides an explicit host-local Ansible workflow for native `caddy.service` on
80/443. It is separate from application delivery, Jenkins maintenance and the
CLI VM's `battlefield-mcp-proxy.service`. Both proxies use `/usr/bin/caddy`;
this workflow neither upgrades that shared binary nor replaces either unit.

On 2026-10-02, inspection at deployed main `a767fa5` found that the local
application, Grafana and Kibana still used retained Kind on `127.0.0.1:8888`.
The old application replicas were intentionally zero after the K3s migration,
so its local alias returned 503. The old monitoring pages still responded.
The owner chose the following correction; it takes effect only after a reviewed
`migrate` apply, not when this code merges or ordinary main delivery runs:

| Local HTTPS name | Reviewed destination |
| --- | --- |
| `battlefield.home.arpa` | HTTP 307 to the registered production `BF_URL`, currently `https://play.ordinalfrontier.com`, preserving path/query and Cloudflare Access login |
| `grafana.home.arpa` | Registered K3s private NodePort 30080, Host `grafana.localhost` |
| `kibana.home.arpa` | Same K3s gateway, Host `kibana.localhost` |
| `sonarqube.home.arpa` | Existing `127.0.0.1:9000` |

No local application authentication fallback or Cloudflare bypass is added.
Historical Kind logs remain on retained Kind storage; current K3s monitoring
has its own data. Neither cluster nor any workload is deleted or scaled here.

## Prerequisites and capture

Run as the registered operator on the Ubuntu server. Keep Jenkins main/platform
jobs idle during plan/apply. The wrapper holds the shared host operation lock,
uses the existing host inventory and prompts for sudo with the compatible
executable selected by the host bootstrap. Jenkins is not needed for this step.

Capture requires the existing native Caddy service active/enabled, its packaged
unit without overrides, and an exact match between active configuration and
`/etc/caddy/Caddyfile`. Supported configuration is deliberately narrow: four
owned routes plus self-contained internal-TLS loopback proxy routes. Unknown
directives, imports, external key paths, foreign units and configuration drift
stop before adoption; retain them for explicit review instead of deleting them.

```bash
private_dir="$HOME/.config/autonomous-battlefield/server"
umask 077
python3 scripts/infra/https.py capture \
  --vault "$private_dir/https-inputs.vault"
```

The command prompts for sudo and a new Vault password. An existing private
`--vault-password-file /private/path` can supply the latter. Never put passwords
in arguments or Git. Outputs must be fresh paths outside the checkout.
Capture encrypts the exact Caddyfile, Caddy version/native-unit digest, and the
matching root certificate/private key from
`/var/lib/caddy/.local/share/caddy/pki/authorities/local/`. It exports only the
public certificate beside the Vault as `https-inputs.vault.root.crt`.

Capture verifies TLS against that root and records each route's HTTP status.
It can archive a matching 503 from an existing upstream, reporting
`upstreams_healthy: false` and `adoption_verified: false`. That is recovery
inventory, not a healthy adoption. TLS failures or a mismatch between the proxy
and direct upstream still stop capture. Plan/apply for adoption and restoration
require healthy routes. Migration additionally requires healthy new monitoring
destinations and verifies the application redirect.

Keep the encrypted Vault and its passphrase separately. There is no newly
scheduled or off-host backup. Restoring the root pair preserves browser trust;
Caddy can issue new intermediate/leaf certificates beneath that root. Its
[persistent data directory](https://caddyserver.com/docs/conventions#data-directory)
must not be treated as disposable cache.

## Review and apply

Use a clean checkout at the full reviewed `origin/main` SHA. Capture itself may
run read-only from a staged source snapshot; mutation may not. The following
example is for the current Kind-to-K3s route correction:

```bash
revision="$(git rev-parse origin/main)"
private_dir="$HOME/.config/autonomous-battlefield/server"
python3 scripts/infra/https.py plan --mode migrate --k3s-routes \
  --revision "$revision" --vault "$private_dir/https-inputs.vault" \
  --report "$private_dir/https-plan.json"
```

`--k3s-routes` reads the existing production registration; it verifies the live
cluster UID, ready single-node identity and private Envoy gateway, and derives
the gateway address and public origin. It does not copy Kubernetes credentials
into the HTTPS capture. Review `changes`, `routes`, `ca_sha256`, current `tls`
and `target_upstreams`. For migration, the current application can show 503;
the desired application must be the public redirect, with Grafana and Kibana
using the registered K3s address. SonarQube and extra supported local routes
must remain unchanged.

Within 30 minutes, apply that exact plan:

```bash
python3 scripts/infra/https.py apply --mode migrate --k3s-routes \
  --revision "$revision" --vault "$private_dir/https-inputs.vault" \
  --review "$private_dir/https-plan.json" \
  --report "$private_dir/https-apply.json"
python3 scripts/infra/https.py verify --mode migrate --k3s-routes \
  --revision "$revision" --vault "$private_dir/https-inputs.vault" \
  --report "$private_dir/https-verify.json"
python3 scripts/infra/https.py plan --mode migrate --k3s-routes \
  --revision "$revision" --vault "$private_dir/https-inputs.vault" \
  --report "$private_dir/https-repeat-plan.json"
```

The repeat plan must have `changes: []`; all reported routes must verify TLS.
Use fresh report names for later attempts. Each action prompts for Vault/sudo
unless the existing private password-file options are supplied. A receipt binds
the revision, input contents, routing identities, host and live observation;
expired, replayed or changed plans cannot apply. The root-only ownership state
is `/var/lib/battlefield-https`.

Migration saves the original Caddyfile, writes only the three reviewed route
blocks and reloads native Caddy. The root pair, service unit, CLI proxy and
unrelated route bytes remain in place. A failed reload/verification attempts
to restore the exact original configuration and reload it. Retained journal
state blocks retries if rollback or finalization does not complete; inspect
the original, current and active configurations before manual recovery.

`--mode adopt` records ownership of an already matching, healthy capture
without changing the proxy or CA. `--mode install` is for a clean replacement
host after the CLI/CI/application services have been restored: the shared Caddy
package/version and native unit must match the capture, the unused native unit
must be masked as established by CLI bootstrap, its Caddyfile must be the
packaged default, and no local CA or foreign configuration may exist.
Use `--k3s-routes` with install when restoring the reviewed K3s destinations
from the original pre-migration capture. Installation restores the captured root
before starting Caddy, publishes the public host trust certificate and verifies
TLS. Startup failure masks/stops only native Caddy and retains the incomplete
journal. No automatic CA rotation, ownership reset, package upgrade, destroy
or replacement of an existing installation is provided.

## Workstation trust and DNS

Each workstation must resolve the four names to the Ubuntu server's LAN address
(currently `192.168.1.126`) through its existing local DNS or hosts file. This
workflow does not replace workstation/network DNS. Confirm resolution with
`getent hosts battlefield.home.arpa grafana.home.arpa kibana.home.arpa sonarqube.home.arpa`.
The public application hostname continues to use normal public DNS.

Retain the existing trusted Caddy root in the OS/browser. A new workstation can
import the exported **public** `.root.crt` after independently comparing its
SHA-256 fingerprint with the reviewed capture. Never import or copy `root.key`
to a browser. On Linux/WSL, verify without disabling TLS validation:

```bash
curl --cacert "$private_dir/https-inputs.vault.root.crt" \
  --resolve battlefield.home.arpa:443:192.168.1.126 \
  --dump-header - --output /dev/null https://battlefield.home.arpa/
```

Expect 307 and the exact registered public origin after migration. Following
that redirect invokes normal Cloudflare login; a local TLS check does not prove
authenticated application access. Verify that separately with the existing
production smoke checks. Browser trust stores can require their own import.

## Disposable verification

`scripts/test/test-local-https.py` is part of `test-deployment.sh` through the
server bootstrap harness. It checks route scope, CA/key validation, stale
receipts, preservation/rollback, capture-versus-acceptance behavior and actual
Ansible handling of literal private JSON values.

On the libvirt controller, use the existing pinned Ubuntu 26.04 image:

```bash
python3 scripts/infra/rehearse-https.py \
  --image /private/pinned-ubuntu-26.04.img \
  --evidence-dir /private/new-https-evidence
```

The fixture needs 5 GiB available RAM, 20 GiB free libvirt storage and the active
default NAT network. Its 2-vCPU/3-GiB/16-GiB guest uses a synthetic CA and
upstreams to exercise real Ansible adoption, a retired Kind 503, the redirect
and monitoring migration, fresh restore, repeat apply and reboot. Negative
checks cover missing keys, foreign files, identity drift and stale/replayed
plans. Evidence includes source hashes and preserved hypervisor identities;
cleanup removes only the fixture VM and owned volumes. These checks do not
substitute for the reviewed live migration or a full clean-host rebuild.
