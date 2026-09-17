const assert = require('node:assert/strict');
const {test} = require('node:test');
const {healthy} = require('../lib/prometheus-targets.cjs');
test('require all current API replicas, correct pod IPs and fresh scrape results', () => {
  const now = Date.now();
  const deployment = {metadata:{generation:3}, spec:{replicas:2}, status:{observedGeneration:3,updatedReplicas:2,readyReplicas:2}};
  const pods = {items: ['one','two'].map((name,i) => ({metadata:{name},status:{phase:'Running',podIP:`10.0.0.${i+1}`,conditions:[{type:'Ready',status:'True'}]}}))};
  const targets = {status:'success',data:{activeTargets:pods.items.map(p => ({labels:{job:'battlefield-pods',component:'api',scrape_pod:p.metadata.name},health:'up',lastScrape:new Date(now).toISOString(),scrapeUrl:`http://${p.status.podIP}:8080/metrics`}))}};
  const check = (d,p,t) => healthy(d,p,t,now);
  assert.equal(check(deployment,pods,targets), true);
  for (const edit of [
    t => t.data.activeTargets.pop(),
    t => {t.data.activeTargets[1].health='down';},
    t => {t.data.activeTargets[1].labels.scrape_pod='old-pod';},
    t => {t.data.activeTargets[1].scrapeUrl='http://10.0.0.9:8080/metrics';},
    t => {t.data.activeTargets[1].lastScrape=new Date(now-61000).toISOString();},
    t => {t.data.activeTargets[1].lastScrape='invalid';},
    t => {t.status='error';},
  ]) {
    const copy=structuredClone(targets); edit(copy);
    assert.equal(check(deployment,pods,copy),false);
  }
  assert.equal(check({...deployment,spec:{replicas:3}},pods,targets),false);
  assert.equal(check({...deployment,status:{...deployment.status,observedGeneration:2}},pods,targets),false);
  const pending=structuredClone(pods);pending.items[1].status.conditions=[];
  assert.equal(check(deployment,pending,targets),false);
  const terminating=structuredClone(pods);terminating.items.push({metadata:{name:'old',deletionTimestamp:'now'}});
  assert.equal(check(deployment,terminating,targets),true);
});
