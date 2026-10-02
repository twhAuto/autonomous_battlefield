# Infrastructure

Deployment assets, standalone development services and platform provisioning live
here. Application source stays in `backend/` and `frontend/`; executable workflows
live in `scripts/`. See the [documentation index](../docs/README.md) for runbooks.

| Directory | Ownership and entry point |
| --- | --- |
| [`deploy/`](deploy/) | Kubernetes bases/overlays, image admission, CLI worker assets and [deployment policies](deploy/policy/README.md); [deployment guide](../docs/operations/deployment.md) |
| [`local/`](local/) | Standalone PostgreSQL with optional Redis/DBHub; [local setup](../docs/developer/local-setup.md#standalone-development-services) |
| [`ansible/`](ansible/) | Host and service configuration; [server bootstrap](../docs/operations/server-bootstrap.md) |
| [`libvirt/`](libvirt/) | Persistent VM, network and disks; [K3s VM](../docs/operations/k3s-vm.md) |
| [`k3s/`](k3s/) | Cluster configuration and platform pins; [K3s platform](../docs/operations/k3s-platform.md) |
| [`jenkins/`](jenkins/) | Auxiliary pipelines, job definitions and plugin pins; [platform jobs](../docs/operations/platform-jobs.md) |
| [`sonarqube/`](sonarqube/) | Local analysis service; [Jenkins operations](../docs/operations/jenkins.md#configure-local-sonarqube) |
| [`aws/`](aws/README.md) | Separate Terraform reference for AWS |

Kind is the normal local application environment. Production uses the registered
K3s environment and verified image delivery. The root `Jenkinsfile` owns the
application pipeline; infrastructure administration has separate entry points.
