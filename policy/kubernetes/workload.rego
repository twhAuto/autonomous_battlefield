package kubernetes.workload

import rego.v1

workload if input.kind in {"Deployment", "StatefulSet", "DaemonSet", "Job", "CronJob"}

pod_spec := input.spec.template.spec if input.kind != "CronJob"
pod_spec := input.spec.jobTemplate.spec.template.spec if input.kind == "CronJob"

# Third-party observability components have their own upstream constraints.
# This policy's blocking contract covers images built from this repository.
protected if {
	workload
	some container in pod_spec.containers
	startswith(container.image, "autonomous-battlefield-")
}

deny contains msg if {
	protected
	not pod_spec.securityContext.runAsNonRoot
	msg := sprintf("%s/%s must set pod runAsNonRoot=true", [input.kind, input.metadata.name])
}

deny contains msg if {
	protected
	pod_spec.securityContext.seccompProfile.type != "RuntimeDefault"
	msg := sprintf("%s/%s must use RuntimeDefault seccomp", [input.kind, input.metadata.name])
}

deny contains msg if {
	protected
	some container in pod_spec.containers
	endswith(container.image, ":latest")
	msg := sprintf("%s/%s container %s uses a latest tag", [input.kind, input.metadata.name, container.name])
}

deny contains msg if {
	protected
	some container in pod_spec.containers
	container.securityContext.privileged == true
	msg := sprintf("%s/%s container %s is privileged", [input.kind, input.metadata.name, container.name])
}

deny contains msg if {
	protected
	some container in pod_spec.containers
	container.securityContext.allowPrivilegeEscalation != false
	msg := sprintf("%s/%s container %s must disable privilege escalation", [input.kind, input.metadata.name, container.name])
}

deny contains msg if {
	protected
	some container in pod_spec.containers
	not container.resources.requests
	msg := sprintf("%s/%s container %s has no resource requests", [input.kind, input.metadata.name, container.name])
}

deny contains msg if {
	protected
	some container in pod_spec.containers
	not container.resources.limits
	msg := sprintf("%s/%s container %s has no resource limits", [input.kind, input.metadata.name, container.name])
}

deny contains msg if {
	protected
	input.kind == "Deployment"
	some container in pod_spec.containers
	not container.readinessProbe
	msg := sprintf("Deployment/%s container %s has no readiness probe", [input.metadata.name, container.name])
}
