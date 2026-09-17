// Verify fresh scrapes for every current API pod, including replicas added by the HPA.
const fs = require('node:fs');

function healthy(deployment, pods, response, now = Date.now()) {
  const desired = deployment.spec?.replicas;
  const status = deployment.status || {};
  if (!Number.isInteger(desired) || desired < 1 ||
      !Number.isInteger(status.observedGeneration) || !Number.isInteger(deployment.metadata?.generation) ||
      status.observedGeneration < deployment.metadata.generation ||
      !(status.updatedReplicas >= desired) || !(status.readyReplicas >= desired)) return false;
  const current = pods.items?.filter(p => !p.metadata?.deletionTimestamp);
  if (!current || current.length < desired || response.status !== 'success' ||
      !Array.isArray(response.data?.activeTargets)) return false;
  return current.every(p => p.status?.phase === 'Running' &&
    p.status?.conditions?.some(c => c.type === 'Ready' && c.status === 'True') &&
    response.data.activeTargets.some(t => {
      const age = now - Date.parse(t.lastScrape);
      return t.labels?.job === 'battlefield-pods' && t.labels.component === 'api' &&
        t.labels.scrape_pod === p.metadata.name && t.health === 'up' &&
        t.scrapeUrl === `http://${p.status.podIP}:8080/metrics` &&
        Number.isFinite(age) && age >= -5000 && age <= 60000;
    }));
}

if (require.main === module) {
  try {
    process.exit(healthy(...process.argv.slice(2, 5).map(p => JSON.parse(fs.readFileSync(p)))) ? 0 : 1);
  } catch { process.exit(1); }
}
module.exports = {healthy};
