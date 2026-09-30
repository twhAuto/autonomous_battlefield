"""Make the official controller install manifest converge under Ansible ownership."""
import hashlib
import json
import yaml


def controller_manifest(content):
    documents = [document for document in yaml.safe_load_all(content) if document]
    for document in documents:
        if document.get('kind') == 'Job' and 'ttlSecondsAfterFinished' in document.get('spec', {}):
            if document['metadata']['name'] != 'eg-gateway-helm-certgen' or document['metadata'].get('namespace') != 'envoy-gateway-system':
                raise ValueError('Review lifecycle ownership of new self-deleting controller jobs')
            # Helm hooks self-delete. SSA instead retains the completed job, and
            # a changed spec gets a new name rather than patching an immutable Job.
            del document['spec']['ttlSecondsAfterFinished']
            digest = hashlib.sha256(json.dumps(document['spec'], sort_keys=True).encode()).hexdigest()[:12]
            document['metadata']['name'] += '-' + digest
    return {'apiVersion': 'v1', 'kind': 'List', 'items': documents}


class FilterModule:
    def filters(self):
        return {'battlefield_k3s_controller': controller_manifest}
