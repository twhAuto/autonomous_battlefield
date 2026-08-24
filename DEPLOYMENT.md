# Deployment learning guide

This guide records the deployment direction for Autonomous Battlefield and the
recommended path from local Kubernetes to AWS. The goal is to learn the native
Kubernetes concepts first while keeping the application portable enough to run
on Amazon EKS without rewriting it.

The application is not Kubernetes-ready yet. It still needs production
container images and Kubernetes manifests. The first local session below only
creates and verifies a local cluster; the following milestone packages and
deploys this application.

## Deployment direction

Use the same application containers and core Kubernetes resources locally and
on AWS:

```text
Browser
   |
   v
Frontend / reverse proxy
   |
   v
Go API Deployment ------> PostgreSQL
                              ^
                              |
                       Engine Deployment
```

The API and engine are separate workloads. PostgreSQL remains the durable
source of truth and battle queue. Do not introduce SQS merely to deploy the
current application; it can be a later architecture exercise if the queue
requirements change.

For a first AWS deployment, use:

- Amazon ECR for container images.
- Amazon EKS for Kubernetes.
- Amazon RDS for PostgreSQL.
- An Application Load Balancer for HTTPS ingress.
- AWS Secrets Manager for database credentials.
- CloudWatch or OpenTelemetry for operational signals.
- Route 53 and ACM when a public domain and TLS certificate are needed.

Keep PostgreSQL outside EKS in AWS. Running a production database inside the
cluster introduces storage, backup, failover, and upgrade responsibilities that
are separate from the application-deployment exercise.

## Portable base and environment overlays

Start with native YAML resources and organize them with Kustomize:

```text
deploy/
├── base/
│   ├── namespace.yaml
│   ├── configmap.yaml
│   ├── migration-job.yaml
│   ├── api-deployment.yaml
│   ├── api-service.yaml
│   ├── engine-deployment.yaml
│   ├── frontend-deployment.yaml
│   ├── frontend-service.yaml
│   └── kustomization.yaml
└── overlays/
    ├── local/
    │   └── kustomization.yaml
    └── aws/
        ├── ingress.yaml
        └── kustomization.yaml
```

The base should contain standard Kubernetes behavior. Provider-specific
annotations, resource identifiers, and secret integrations belong in the AWS
overlay.

| Portable Kubernetes resource | Local implementation | AWS implementation |
| --- | --- | --- |
| Container image | Image loaded into `kind` | Image pushed to ECR |
| API and engine | `Deployment` | The same `Deployment` on EKS |
| Internal networking | `Service` DNS | The same `Service` DNS on EKS |
| Database | Disposable development PostgreSQL | RDS PostgreSQL |
| Database credential | Uncommitted local Secret | Secrets Manager integration |
| Public access | `kubectl port-forward` initially | ALB-backed Ingress |
| Non-secret settings | `ConfigMap` | The same `ConfigMap`, with an overlay if needed |

Kustomize is already included in `kubectl`. It can render or apply an overlay
without introducing another package manager:

```bash
kubectl kustomize deploy/overlays/local
kubectl apply -k deploy/overlays/local
```

## Why Helm comes later

Do not begin by translating every resource into a Helm template. First write,
deploy, inspect, and troubleshoot the Kubernetes resources directly. This keeps
the revision exercise focused on what Kubernetes itself is doing.

Add a Helm chart later when one or more of these becomes true:

- Other users need to install the application as a packaged release.
- Many deployments need configurable domains, images, replicas, resources, and
  feature settings.
- The project needs chart versions and Helm install, upgrade, and rollback
  workflows.
- Kustomize overlays become repetitive or difficult to maintain.

Converting working resources into a chart is then a useful packaging exercise,
not a prerequisite for the first deployment.

## First local Kubernetes session

The recommended local cluster is
[`kind`](https://kind.sigs.k8s.io/docs/user/quick-start/). It runs Kubernetes
nodes as Docker containers and works well for application development and
deployment exercises.

### 1. Verify the existing prerequisites

From WSL, confirm Docker and `kubectl` are usable:

```bash
docker info
kubectl version --client
```

If `docker info` cannot reach the daemon, start Docker Desktop and enable its
WSL integration for the current distribution before continuing.

### 2. Install `kind`

With Go already installed, install the current pinned `kind` release:

```bash
go install sigs.k8s.io/kind@v0.32.0
export PATH="$(go env GOPATH)/bin:$PATH"
kind version
```

Add the Go binary directory to the shell profile if it is not already on
`PATH`. The upstream quick-start page should be used when intentionally updating
the pinned version.

### 3. Create and inspect the cluster

```bash
kind create cluster --name battlefield
kubectl cluster-info --context kind-battlefield
kubectl get nodes
kubectl get pods --all-namespaces
```

`kind` writes the cluster connection to the normal kubeconfig and selects the
new context. Confirm it before applying resources:

```bash
kubectl config current-context
```

The expected context is `kind-battlefield`.

### 4. Run a disposable smoke test

This verifies scheduling, image pulling, service discovery, and port forwarding
before project-specific resources are introduced:

```bash
kubectl create deployment k8s-smoke --image=nginx:1.29-alpine
kubectl expose deployment k8s-smoke --port=80
kubectl rollout status deployment/k8s-smoke
kubectl port-forward service/k8s-smoke 8088:80
```

While the port-forward command is running, open `http://localhost:8088`. Stop
the port forward with Ctrl+C, then remove the smoke-test resources:

```bash
kubectl delete service/k8s-smoke deployment/k8s-smoke
```

### 5. Stop or remove the cluster

A `kind` cluster runs as Docker containers. Delete it when the exercise is
finished:

```bash
kind delete cluster --name battlefield
```

Recreating a local cluster is expected and provides useful practice proving
that configuration is reproducible rather than dependent on manual changes.

## Application Kubernetes milestone

After the cluster smoke test, package the application in this order:

1. Add a multi-stage backend image containing the API and engine binaries,
   `config.yaml`, and `scenarios/`.
2. Add a multi-stage frontend image that builds Vite assets and serves them
   through a non-root web server or reverse proxy.
3. Add the Kustomize base and local overlay.
4. Add disposable local PostgreSQL for the learning environment. Do not treat
   it as the production database design.
5. Load locally built images into `kind`:

   ```bash
   kind load docker-image autonomous-battlefield-backend:dev --name battlefield
   kind load docker-image autonomous-battlefield-frontend:dev --name battlefield
   ```

6. Run the migration Job and require it to complete successfully before the API
   and engine rollout.
7. Apply the local overlay and inspect the result:

   ```bash
   kubectl apply -k deploy/overlays/local
   kubectl get pods,services,jobs -n battlefield
   kubectl logs -n battlefield deployment/battlefield-api
   kubectl logs -n battlefield deployment/battlefield-engine
   ```

8. Reach the frontend initially with `kubectl port-forward`. Add an ingress
   controller only after the core workloads work.

## Repository-specific requirements

- Run migrations once through a Kubernetes `Job` using the existing
  `server -migrate` mode. Never let every API replica race to migrate.
- Pass an explicit configuration path in containers, for example
  `-config /etc/battlefield/config.yaml`.
- Scenario paths are resolved relative to the configuration file. If the
  ConfigMap is mounted under `/etc/battlefield`, set
  `api.scenarioCatalogPath` to an absolute image path such as
  `/app/scenarios`.
- Use `/livez` for engine liveness and `/readyz` for engine readiness on port
  8081.
- Add a database-aware API `/readyz`; its current `/healthz` only proves that
  the HTTP process is alive.
- Set `terminationGracePeriodSeconds` longer than the configured ten-second
  graceful-shutdown deadline.
- The engine can have multiple replicas because work is claimed through durable
  database leases. Each pod still requires a unique worker identity; its pod
  name or UID can be injected with the Downward API.
- Account for database-pool multiplication. Each replica currently permits up
  to ten connections, so the total possible connections grow with every API and
  engine pod.
- Keep `DATABASE_URL` out of Git, image layers, ConfigMaps, logs, and command
  examples containing real credentials.

## Scaling and reliability exercises

Once one replica works, exercise these behaviors deliberately:

- Scale the API and engine separately.
- Terminate an engine pod during a battle and verify requeue or lease recovery.
- Verify readiness removes a pod from service without immediately restarting it.
- Verify liveness restarts a genuinely stuck process.
- Perform a rolling update and rollback.
- Add CPU and memory requests based on observations.
- Scale the API from a request or CPU signal and the engine from battle backlog
  or oldest queued work rather than CPU by default.
- Add a Pod Disruption Budget only to workloads with enough replicas to remain
  available during disruption.
- Recreate the entire cluster and application from committed configuration.

## Moving the deployment to AWS

The AWS step should reuse the tested images and base resources:

1. Create the AWS account, cost budget, and billing alerts.
2. Provision ECR, networking, EKS, RDS, and Secrets Manager through
   infrastructure as code.
3. Push immutable, commit-addressed images to ECR.
4. Configure EKS workload identity and least-privilege access to the required
   secret.
5. Point `DATABASE_URL` at the private RDS endpoint with TLS enabled.
6. Add the AWS Load Balancer configuration in the AWS overlay.
7. Run the migration Job and deploy the workloads.
8. Exercise secret rotation, backup restore, pod disruption, failed rollout,
   and teardown.

Amazon EKS itself is billable. A standard-support cluster has an hourly control
plane charge in addition to compute, load balancer, database, networking, and
storage charges. Learn locally first, create AWS Budgets before provisioning,
and destroy learning environments when they are not in use. Budget alerts are
notifications rather than guaranteed hard spending limits.

## Reference documentation

- [`kind` quick start](https://kind.sigs.k8s.io/docs/user/quick-start/)
- [Kustomize with `kubectl`](https://kubernetes.io/docs/tasks/manage-kubernetes-objects/kustomization/)
- [Kubernetes probes](https://kubernetes.io/docs/concepts/workloads/pods/probes/)
- [Amazon EKS pricing](https://aws.amazon.com/eks/pricing/)
- [AWS Free Tier plans](https://docs.aws.amazon.com/awsaccountbilling/latest/aboutv2/free-tier-plans.html)
- [AWS Budgets](https://docs.aws.amazon.com/cost-management/latest/userguide/create-cost-budget.html)
- [EKS Auto Mode](https://docs.aws.amazon.com/eks/latest/userguide/automode.html)
- [Using Secrets Manager secrets with EKS pods](https://docs.aws.amazon.com/eks/latest/userguide/manage-secrets.html)

