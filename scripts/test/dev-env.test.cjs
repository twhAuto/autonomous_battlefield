const {test} = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const {main, read, validate, profile, group} = require('../lib/application-env.cjs');

test('migration preserves literal credentials and extraction isolates Secrets', () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'battlefield-env-test-'));
  try {
    const database = path.join(dir,'database'); const auth = path.join(dir,'auth');
    const ai = path.join(dir,'ai'); const target = path.join(dir,'.env');
    fs.writeFileSync(database, 'POSTGRES_DB=battlefield\r\nPOSTGRES_USER=battlefield\r\nPOSTGRES_PASSWORD=p=a#ss$word\r\nDATABASE_URL=postgres://example\r\n');
    fs.writeFileSync(auth, 'BATTLEFIELD_ADMIN_PASSWORD=a long $(literal) password\nBATTLEFIELD_AUTH_MODE=local-password\nBATTLEFIELD_ADMIN_USERNAME=admin\nBATTLEFIELD_SESSION_SECRET='+'a'.repeat(32)+'\n');
    fs.writeFileSync(ai, 'OPENAI_API_KEY=key\nBATTLEFIELD_AGENT_SECRET='+ 'w'.repeat(32)+'\nBATTLEFIELD_MCP_CAPABILITY_SECRET='+ 'm'.repeat(32)+'\n');
    main(['merge',target,database,auth,ai]);
    main(['validate',target]);
    assert.equal(read(target).get('POSTGRES_PASSWORD'), 'p=a#ss$word');
    assert.equal(read(target).get('BATTLEFIELD_ADMIN_PASSWORD'), 'a long $(literal) password');
    assert.equal(fs.statSync(target).mode & 0o777, 0o600);
    const before = fs.readFileSync(target);
    assert.throws(()=>main(['merge',target,database]), /already exists/);
    assert.deepEqual(fs.readFileSync(target), before);
    for (const [group, original] of [['database',database],['auth',auth],['ai',ai]]) {
      const output=path.join(dir, group+'-out');
      main(['extract',target,output,group]);
      const actual=read(output);
      for(const [key,value] of read(original)) assert.equal(actual.get(key),value);
      for(const key of actual.keys()) assert.equal(require('../lib/application-env.cjs').group(key),group);
      if(group==='ai') assert.equal(actual.get('GEMINI_PROFILE_AVAILABLE'),'false');
      main(['extract',target,output,group]);
      assert.equal(fs.statSync(output).mode & 0o777,0o600);
    }
    fs.writeFileSync(ai, 'POSTGRES_PASSWORD=different\n');
    assert.throws(()=>main(['merge',path.join(dir,'conflict'),database,ai]), /Conflicting/);
    assert.equal(fs.existsSync(path.join(dir,'conflict')),false);
    fs.writeFileSync(ai,'KEY=one\nKEY=two\n');
    assert.throws(()=>read(ai),/Duplicate/);
    fs.writeFileSync(ai,'export KEY=value\n');
    assert.throws(()=>read(ai),/Invalid/);
    const live = path.join(dir,'live.json'); const recovered = path.join(dir,'recovered');
    fs.writeFileSync(live,JSON.stringify({data:{BATTLEFIELD_ADMIN_PASSWORD:Buffer.from('existing login').toString('base64')}}));
    main(['decode',live,recovered]);
    assert.equal(read(recovered).get('BATTLEFIELD_ADMIN_PASSWORD'),'existing login');
    fs.writeFileSync(live,JSON.stringify({data:{KEY:Buffer.from('bad\nentry').toString('base64')}}));
    assert.throws(()=>main(['decode',live,path.join(dir,'bad')]),/cannot be represented/);
  } finally { fs.rmSync(dir,{recursive:true,force:true}); }
});

test('shared schema enforces production authentication and separates deployment fields', () => {
  const values = new Map(Object.entries({POSTGRES_DB:'battlefield',POSTGRES_USER:'battlefield',POSTGRES_PASSWORD:'password',DATABASE_URL:'postgres://example',
    BATTLEFIELD_AGENT_SECRET:'w'.repeat(32),BATTLEFIELD_MCP_CAPABILITY_SECRET:'m'.repeat(32),
    BATTLEFIELD_AUTH_MODE:'cloudflare',BATTLEFIELD_ACCESS_ISSUER:'https://team.cloudflareaccess.com',
    BATTLEFIELD_ACCESS_AUDIENCE:'a'.repeat(64),BATTLEFIELD_PUBLIC_ORIGIN:'https://play.example.com',
    BATTLEFIELD_ACCESS_ROLES:'{"admin":["owner@example.com"]}'}));
  validate(values,'production');
  values.set('BATTLEFIELD_ACCESS_CI_CLIENT_ID', 'a'.repeat(32)+'.access');
  validate(values,'production');
  assert.equal(group('BATTLEFIELD_ACCESS_CI_CLIENT_ID'), 'auth');
  values.set('BATTLEFIELD_ACCESS_CI_CLIENT_ID', 'owner@example.com');
  assert.throws(()=>validate(values,'production'),/CI client ID/);
  values.delete('BATTLEFIELD_ACCESS_CI_CLIENT_ID');
  values.set('BATTLEFIELD_AUTH_MODE','disabled-development');
  assert.throws(()=>validate(values,'production'),/Cloudflare/);
  validate(values,'dev');
  values.set('BATTLEFIELD_AUTH_ENABLED','true');
  assert.throws(()=>validate(values,'dev'),/explicitly/);
  assert.throws(()=>group('BF_TUNNEL_CREDENTIALS_FILE'),/Unknown application/);
  assert.equal(group('BATTLEFIELD_REDIS_URL'),'cache');
  const dir=fs.mkdtempSync(path.join(os.tmpdir(),'battlefield-profile-'));
  try {
    const file=path.join(dir,'deployment.env');
    fs.writeFileSync(file,'BF_ENV=dev\nBF_CONTEXT=dev\nBF_CLUSTER=battlefield\nBF_UID=uid\nBF_DOCKER_HOST=unix:///var/run/docker.sock\nBF_URL=http://localhost\nBF_KUBECONFIG=kubeconfig\n');
    assert.equal(profile(file).get('BF_KUBECONFIG'),path.join(dir,'kubeconfig'));
    fs.appendFileSync(file,'POSTGRES_PASSWORD=secret\n');
    assert.throws(()=>profile(file),/Unknown deployment/);
  } finally {fs.rmSync(dir,{recursive:true,force:true});}
});

test('managed Player settings require complete Cloudflare config and isolate its token', () => {
  const values = new Map(Object.entries({POSTGRES_DB:'battlefield',POSTGRES_USER:'battlefield',POSTGRES_PASSWORD:'password',DATABASE_URL:'postgres://example',
    BATTLEFIELD_AGENT_SECRET:'w'.repeat(32),BATTLEFIELD_MCP_CAPABILITY_SECRET:'m'.repeat(32),
    BATTLEFIELD_AUTH_MODE:'cloudflare',BATTLEFIELD_ACCESS_ISSUER:'https://team.cloudflareaccess.com',
    BATTLEFIELD_ACCESS_AUDIENCE:'a'.repeat(64),BATTLEFIELD_PUBLIC_ORIGIN:'https://play.example.com',
    BATTLEFIELD_ACCESS_ROLES:'{"admin":["owner@example.com"]}', BATTLEFIELD_MANAGED_PLAYERS:'true'}));
  assert.throws(()=>validate(values,'production'),/Managed Players/);
  values.set('BATTLEFIELD_MEMBERS_ACCOUNT_ID','a'.repeat(32));
  values.set('BATTLEFIELD_MEMBERS_GROUP_ID','aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa');
  values.set('BATTLEFIELD_MEMBERS_API_TOKEN','private-token');
  validate(values,'production');
  for (const email of ['user@[10.0.0.1]', 'a'.repeat(65)+'@example.com']) {
    values.set('BATTLEFIELD_ACCESS_ROLES',JSON.stringify({admin:['owner@example.com'],player:[email]}));
    assert.throws(()=>validate(values,'production'),/managed Player email/);
  }
  values.set('BATTLEFIELD_ACCESS_ROLES','{"admin":["owner@example.com"]}');
  values.set('BATTLEFIELD_MANAGED_PLAYERS','yes');
  assert.throws(()=>validate(values,'production'),/boolean/);
  values.set('BATTLEFIELD_MANAGED_PLAYERS','true');
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'battlefield-members-env-'));
  try {
    const source=path.join(dir,'.env'); fs.writeFileSync(source,[...values].map(([k,v])=>`${k}=${v}\n`).join(''));
    for(const name of ['auth','members','ai','runtime','database']) {
      const out=path.join(dir,name); main(['extract',source,out,name]);
      assert.equal(read(out).has('BATTLEFIELD_MEMBERS_API_TOKEN'),name==='members');
      assert.equal(fs.statSync(out).mode & 0o777,0o600);
    }
  } finally { fs.rmSync(dir,{recursive:true,force:true}); }
});


test('cluster driver defaults to Kind and K3s requires an explicit production storage/TLS contract', () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'battlefield-driver-'));
  const file = path.join(dir, 'deployment.env');
  const base = 'BF_ENV=production\nBF_CONTEXT=k3s\nBF_CLUSTER=node\nBF_UID=uid\nBF_URL=https://play.example.com\nBF_KUBECONFIG=kubeconfig\n';
  try {
    fs.writeFileSync(file, base + 'BF_DOCKER_HOST=unix:///var/run/docker.sock\n');
    assert.equal(profile(file).get('BF_DRIVER'), 'kind');
    fs.writeFileSync(file, base + 'BF_DRIVER=k3s\nBF_KUBELET_CA_FILE=ca.crt\n');
    assert.equal(profile(file).get('BF_DRIVER'), 'k3s');
    assert.equal(profile(file).get('BF_KUBELET_CA_FILE'), path.join(dir, 'ca.crt'));
    for (const [body, error] of [
      [base + 'BF_DRIVER=other\n', /Invalid cluster driver/],
      [base + 'BF_DRIVER=k3s\n', /BF_KUBELET_CA_FILE/],
      [base + 'BF_DRIVER=k3s\nBF_DOCKER_HOST=unix:///var/run/docker.sock\n', /must not bind/],
      [base.replace('production', 'dev') + 'BF_DRIVER=k3s\n', /production profile/],
    ]) {
      fs.writeFileSync(file, body);
      assert.throws(() => profile(file), error);
    }
  } finally { fs.rmSync(dir, {recursive: true, force: true}); }
});
