// Literal Kubernetes env-file format: no shell evaluation, interpolation or unquoting.
const fs = require('node:fs');
const path = require('node:path');
const fields = {
  // MCP_DATABASE_URL is the pre-rename name for AI_GATEWAY_DATABASE_URL.
  database: 'POSTGRES_DB POSTGRES_USER POSTGRES_PASSWORD DATABASE_URL AI_GATEWAY_DATABASE_URL MCP_DATABASE_URL',
  auth: 'BATTLEFIELD_AUTH_MODE BATTLEFIELD_AUTH_ENABLED BATTLEFIELD_ADMIN_USERNAME BATTLEFIELD_ADMIN_PASSWORD BATTLEFIELD_SESSION_SECRET BATTLEFIELD_AUTH_COOKIE_SECURE BATTLEFIELD_ACCESS_ISSUER BATTLEFIELD_ACCESS_AUDIENCE BATTLEFIELD_ACCESS_ROLES BATTLEFIELD_ACCESS_CI_CLIENT_ID BATTLEFIELD_PUBLIC_ORIGIN BATTLEFIELD_ACCESS_GUEST_AUDIENCE BATTLEFIELD_GUEST_ORIGIN',
  ai: 'BATTLEFIELD_AGENT_SECRET BATTLEFIELD_MCP_CAPABILITY_SECRET BATTLEFIELD_CLI_ISOLATION_VERIFIED OPENAI_API_KEY OPENAI_MODEL OPENAI_PROFILE_AVAILABLE GEMINI_API_KEY GEMINI_MODEL GEMINI_PROFILE_AVAILABLE CODEX_CLI_PROFILE_AVAILABLE GEMINI_CLI_PROFILE_AVAILABLE CLAUDE_CLI_PROFILE_AVAILABLE',
  members: 'BATTLEFIELD_MANAGED_PLAYERS BATTLEFIELD_MEMBERS_ACCOUNT_ID BATTLEFIELD_MEMBERS_GROUP_ID BATTLEFIELD_MEMBERS_API_TOKEN',
  cache: 'BATTLEFIELD_REDIS_URL',
  runtime: 'LOG_LEVEL OTEL_EXPORTER_OTLP_ENDPOINT OTEL_EXPORTER_OTLP_TRACES_ENDPOINT OTEL_TRACES_SAMPLER_ARG LANGFUSE_BASE_URL LANGFUSE_PROJECT_ID',
};
const profileFields = 'BF_ENV BF_CONTEXT BF_CLUSTER BF_UID BF_DOCKER_HOST BF_URL BF_KUBECONFIG BF_DISPOSABLE BF_TUNNEL_ID BF_TUNNEL_CREDENTIALS_FILE BF_TUNNEL_IMAGE BF_KUBELET_CA_FILE BF_DRIVER BF_BACKUP_RECIPIENT'.split(' ');

function read(file) {
  const values = new Map();
  for (const line of fs.readFileSync(file, 'utf8').split(/\r?\n/)) {
    if (!line.trim() || line.trimStart().startsWith('#')) continue;
    const match = /^([A-Z_][A-Z0-9_]*)=([^\r\n\0]*)$/.exec(line);
    if (!match) throw new Error('Invalid env-file entry (use literal KEY=value lines).');
    if (values.has(match[1])) throw new Error(`Duplicate setting: ${match[1]}`);
    values.set(match[1], match[2]);
  }
  return values;
}

function group(key) {
  for (const [name, keys] of Object.entries(fields)) if (keys.split(' ').includes(key)) return name;
  throw new Error(`Unknown application setting: ${key}`);
}

// The optional guest Access application needs its own audience and HTTPS origin, set
// together and distinct from the game's.
function validateGuest(values, origin) {
  const audience = values.get('BATTLEFIELD_ACCESS_GUEST_AUDIENCE') || '';
  const guestOrigin = values.get('BATTLEFIELD_GUEST_ORIGIN') || '';
  if (!audience && !guestOrigin) return;
  let guest;
  try { guest = new URL(guestOrigin); } catch { throw new Error('Invalid guest Access audience or origin.'); }
  if (!/^[a-fA-F0-9]{64}$/.test(audience) || audience === values.get('BATTLEFIELD_ACCESS_AUDIENCE') || guest.protocol !== 'https:' || guest.username || guest.password || guest.pathname !== '/' || guest.search || guest.hash || guest.origin === origin.origin) {
    throw new Error('Invalid guest Access audience or origin.');
  }
}

function validate(values, environment) {
  if (!['dev','production'].includes(environment)) throw new Error('Environment must be dev or production.');
  for (const key of values.keys()) group(key);
  const requireLength = (key, minimum = 1) => {
    if (Buffer.byteLength(values.get(key) || '') < minimum) throw new Error(`${key} requires at least ${minimum} bytes.`);
  };
  for (const key of ['DATABASE_URL','POSTGRES_DB','POSTGRES_USER','POSTGRES_PASSWORD']) requireLength(key);
  const gatewayURL = values.get('AI_GATEWAY_DATABASE_URL'), legacyURL = values.get('MCP_DATABASE_URL');
  if (gatewayURL && legacyURL && gatewayURL !== legacyURL) throw new Error('AI_GATEWAY_DATABASE_URL and legacy MCP_DATABASE_URL disagree; set only AI_GATEWAY_DATABASE_URL.');
  for (const key of ['DATABASE_URL','AI_GATEWAY_DATABASE_URL','MCP_DATABASE_URL']) {
    if (!values.get(key)) continue;
    let url;
    try { url = new URL(values.get(key)); } catch { throw new Error(`Invalid database URL: ${key}`); }
    if (!['postgres:','postgresql:'].includes(url.protocol) || !url.hostname) throw new Error(`Invalid database URL: ${key}`);
  }
  for (const key of ['BATTLEFIELD_AGENT_SECRET','BATTLEFIELD_MCP_CAPABILITY_SECRET']) {
    requireLength(key,32);
    // Mirrors requireSecret in backend/internal/config: the binaries refuse these
    // published values, so reject them before any deployment changes the cluster.
    if (values.get(key).startsWith('replace-with-') || values.get(key) === 'local-development-capability-secret-change-me') throw new Error(`${key} uses a published placeholder; generate a random secret.`);
  }
  if (values.get('BATTLEFIELD_AGENT_SECRET') === values.get('BATTLEFIELD_MCP_CAPABILITY_SECRET')) throw new Error('Worker and capability secrets must be distinct.');
  for (const [key,value] of values) {
    if ((key.endsWith('_AVAILABLE') || key.endsWith('_VERIFIED') || ['BATTLEFIELD_AUTH_ENABLED','BATTLEFIELD_AUTH_COOKIE_SECURE'].includes(key)) && value && !['true','false'].includes(value)) throw new Error(`Invalid boolean: ${key}`);
  }
  for (const provider of ['OPENAI','GEMINI']) {
    if (values.get(`${provider}_PROFILE_AVAILABLE`) === 'true') {
      requireLength(`${provider}_API_KEY`); requireLength(`${provider}_MODEL`);
    }
  }
  const managedPlayers = values.get('BATTLEFIELD_MANAGED_PLAYERS');
  if (managedPlayers && !['true','false'].includes(managedPlayers)) throw new Error('Invalid managed Players boolean.');
  if (managedPlayers === 'true') {
    if (values.get('BATTLEFIELD_AUTH_MODE') !== 'cloudflare' || !/^[a-f0-9]{32}$/.test(values.get('BATTLEFIELD_MEMBERS_ACCOUNT_ID') || '') || !/^[a-f0-9]{8}(?:-[a-f0-9]{4}){3}-[a-f0-9]{12}$/.test(values.get('BATTLEFIELD_MEMBERS_GROUP_ID') || '') || !(values.get('BATTLEFIELD_MEMBERS_API_TOKEN') || '').trim()) throw new Error('Managed Players require Cloudflare authentication, account ID, group UUID and API token.');
  }
  const mode = values.get('BATTLEFIELD_AUTH_MODE');
  const enabled = values.get('BATTLEFIELD_AUTH_ENABLED');
  if (environment === 'production' || mode === 'cloudflare') {
    if (mode !== 'cloudflare' || (enabled && enabled !== 'true')) throw new Error('Production requires Cloudflare authentication.');
    if (!/^https:\/\/[a-z0-9-]+\.cloudflareaccess\.com\/?$/.test(values.get('BATTLEFIELD_ACCESS_ISSUER') || '')) throw new Error('Invalid Access issuer.');
    if (!/^[a-fA-F0-9]{64}$/.test(values.get('BATTLEFIELD_ACCESS_AUDIENCE') || '')) throw new Error('Invalid Access audience.');
    const ciClientID = values.get('BATTLEFIELD_ACCESS_CI_CLIENT_ID');
    if (ciClientID && !/^[a-f0-9]{32}\.access$/.test(ciClientID)) throw new Error('Invalid Access CI client ID.');
    let origin, roles;
    try { origin = new URL(values.get('BATTLEFIELD_PUBLIC_ORIGIN')); roles = JSON.parse(values.get('BATTLEFIELD_ACCESS_ROLES')); } catch { throw new Error('Invalid Access origin or roles.'); }
    if (origin.protocol !== 'https:' || origin.username || origin.password || origin.pathname !== '/' || origin.search || origin.hash) throw new Error('Invalid Access origin.');
    validateGuest(values, origin);
    const seen = new Set();
    if (!roles || !Array.isArray(roles.admin) || !roles.admin.length) throw new Error('Assign at least one Access admin.');
    for (const [role, emails] of Object.entries(roles)) {
      if (!['admin','tester','player'].includes(role) || !Array.isArray(emails)) throw new Error('Invalid Access role assignment.');
      for (const email of emails) {
        if (typeof email !== 'string' || !/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(email) || seen.has(email.toLowerCase())) throw new Error('Invalid or duplicate Access identity.');
        if (managedPlayers === 'true' && role === 'player') {
          const at = email.lastIndexOf('@');
          const domain = email.slice(at + 1).toLowerCase();
          if (email.length > 254 || at > 64 || !/^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)+$/.test(domain)) throw new Error('Invalid managed Player email.');
        }
        seen.add(email.toLowerCase());
      }
    }
    if (['CODEX_CLI_PROFILE_AVAILABLE','GEMINI_CLI_PROFILE_AVAILABLE','CLAUDE_CLI_PROFILE_AVAILABLE'].some(k=>values.get(k)==='true') && values.get('BATTLEFIELD_CLI_ISOLATION_VERIFIED') !== 'true') throw new Error('Production CLI isolation must be verified.');
    return;
  }
  if (mode === 'disabled-development' && enabled !== 'true') return;
  if (mode !== 'local-password' || enabled === 'false') throw new Error('Configure development authentication explicitly.');
  requireLength('BATTLEFIELD_ADMIN_USERNAME'); requireLength('BATTLEFIELD_ADMIN_PASSWORD',12); requireLength('BATTLEFIELD_SESSION_SECRET',32);
}

function profile(file) {
  const values = read(file);
  for (const key of values.keys()) if (!profileFields.includes(key)) throw new Error(`Unknown deployment setting: ${key}`);
  // Profiles created before driver selection describe Kind.
  if (!values.has('BF_DRIVER')) values.set('BF_DRIVER', 'kind');
  const driver = values.get('BF_DRIVER');
  if (!['kind', 'k3s'].includes(driver)) throw new Error('Invalid cluster driver.');
  for (const key of ['BF_ENV','BF_CONTEXT','BF_CLUSTER','BF_UID','BF_URL','BF_KUBECONFIG']) {
    if (!values.get(key)) throw new Error(`Missing deployment setting: ${key}`);
  }
  if (driver === 'kind' && !values.get('BF_DOCKER_HOST')) throw new Error('Missing deployment setting: BF_DOCKER_HOST');
  if (driver === 'k3s') {
    if (values.get('BF_ENV') !== 'production') throw new Error('K3s delivery requires a production profile.');
    if (values.get('BF_DOCKER_HOST')) throw new Error('K3s profiles must not bind a Docker daemon.');
    if (!values.get('BF_KUBELET_CA_FILE')) throw new Error('K3s requires BF_KUBELET_CA_FILE.');
  }
  if (!['dev','production'].includes(values.get('BF_ENV'))) throw new Error('Invalid deployment environment.');
  if (values.has('BF_DISPOSABLE') && !['0','1'].includes(values.get('BF_DISPOSABLE'))) throw new Error('Invalid disposable flag.');
  if (values.get('BF_ENV') === 'production' && values.get('BF_DISPOSABLE') === '1') throw new Error('Production cannot be disposable.');
  if (values.get('BF_TUNNEL_ID') && !/^[a-f0-9]{8}(?:-[a-f0-9]{4}){3}-[a-f0-9]{12}$/.test(values.get('BF_TUNNEL_ID'))) throw new Error('Invalid tunnel UUID.');
  if (values.get('BF_TUNNEL_IMAGE') && !/^cloudflare\/cloudflared@sha256:[a-f0-9]{64}$/.test(values.get('BF_TUNNEL_IMAGE'))) throw new Error('Tunnel image must use a verified digest.');
  // Pre-migration dumps are encrypted to this age public key; see kubernetes-deployment.sh.
  if (values.get('BF_BACKUP_RECIPIENT') && !/^age1[qpzry9x8gf2tvdw0s3jn54khce6mua7l]{58}$/.test(values.get('BF_BACKUP_RECIPIENT'))) throw new Error('Backup recipient must be an age public key.');
  for (const key of ['BF_KUBECONFIG','BF_TUNNEL_CREDENTIALS_FILE','BF_KUBELET_CA_FILE']) if (values.get(key)) values.set(key,path.resolve(path.dirname(file),values.get(key)));
  return values;
}

function write(file, values, exclusive = true) {
  fs.writeFileSync(file, [...values].map(([k,v]) => `${k}=${v}\n`).join(''), { mode: 0o600, flag: exclusive ? 'wx' : 'w' });
  fs.chmodSync(file, 0o600);
}

function main([command, source, target, ...inputs]) {
  if (command === 'decode') {
    const data = JSON.parse(fs.readFileSync(source, 'utf8')).data || {};
    const values = Object.entries(data).map(([key, encoded]) => {
      const value = Buffer.from(encoded, 'base64').toString('utf8');
      if (!/^[A-Z_][A-Z0-9_]*$/.test(key) || /[\r\n\0]/.test(value)) throw new Error('Secret cannot be represented as an env file.');
      return [key, value];
    });
    if (!values.length) throw new Error('Existing Secret is empty; recover it before setup.');
    write(target, values);
  } else if (command === 'merge') {
    if (fs.existsSync(source)) throw new Error('Root .env already exists; refusing to overwrite it.');
    const values = new Map();
    for (const file of [target, ...inputs]) {
      for (const [key, value] of read(file)) {
        if (values.has(key) && values.get(key) !== value) throw new Error(`Conflicting setting: ${key}`);
        values.set(key, value);
      }
    }
    write(source, values);
  } else if (command === 'extract') {
    if (source === target) throw new Error('Extraction must not overwrite its source.');
    if (!Object.keys(fields).includes(inputs[0])) throw new Error('Unknown Secret group.');
    const defaults = {
      members: [['BATTLEFIELD_MANAGED_PLAYERS','false']],
      runtime: [['OTEL_EXPORTER_OTLP_ENDPOINT','http://battlefield-otel-collector:4318'],['OTEL_TRACES_SAMPLER_ARG','0.1']],
      ai: [['OPENAI_API_KEY',''],['OPENAI_MODEL',''],['GEMINI_API_KEY',''],['GEMINI_MODEL',''],
        ['OPENAI_PROFILE_AVAILABLE','false'],['GEMINI_PROFILE_AVAILABLE','false'],
        ['CODEX_CLI_PROFILE_AVAILABLE','false'],['GEMINI_CLI_PROFILE_AVAILABLE','false'],['CLAUDE_CLI_PROFILE_AVAILABLE','false'],['BATTLEFIELD_CLI_ISOLATION_VERIFIED','false']],
    };
    const values = new Map(defaults[inputs[0]] || []);
    for (const [key,value] of read(source)) if (group(key) === inputs[0]) values.set(key,value);
    write(target, values, false);
  } else if (command === 'validate') {
    validate(read(source),target || 'dev');
  } else if (command === 'profile' || command === 'json') {
    const values = command === 'profile' ? profile(source) : read(source);
    process.stdout.write(command === 'profile' ? [...values].map(([k,v])=>`${k}=${v}`).join('\n') : JSON.stringify(Object.fromEntries(values)));
  } else throw new Error('Unknown dev-env command.');
}

if (require.main === module) {
  try { main(process.argv.slice(2)); } catch (error) { console.error(error.message); process.exitCode = 1; }
}
module.exports = {read, write, group, validate, profile, main};
