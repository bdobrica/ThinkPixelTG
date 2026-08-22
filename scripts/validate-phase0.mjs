#!/usr/bin/env node

import { createHash } from 'node:crypto';
import { existsSync, readFileSync, readdirSync, statSync } from 'node:fs';
import { dirname, join, normalize, resolve } from 'node:path';

const root = resolve(import.meta.dirname, '..');
const errors = [];
const required = [
  'docs/adr/0000-template.md',
  'docs/architecture/system-context.md',
  'docs/architecture/glossary-and-ownership.md',
  'docs/security/threat-model.md',
  'docs/security/data-classification.md',
  'docs/security/phase-0-authority-review.md',
  'docs/contracts/tool-catalog.md',
  'docs/contracts/invocation-state-machine.md',
  'docs/contracts/canonical-json.md',
  'docs/contracts/retry-idempotency.md',
  'docs/contracts/thinkpixelag-authorization.md',
  'docs/contracts/thinkpixelag-approval.md',
  'docs/contracts/thinkpixelgr.md',
  'docs/contracts/credential-provider.md',
  'docs/contracts/connector.md',
  'docs/contracts/rest-api.md',
  'docs/contracts/mcp.md',
  'docs/contracts/postgresql-schema.sql',
  'docs/contracts/postgresql-transactions.md',
  'docs/contracts/evidence.md',
  'docs/supported-versions.md',
  'docs/operations/slos-and-capacity.md',
  'api/openapi.yaml',
];

for (const path of required) {
  if (!existsSync(join(root, path))) errors.push(`missing required artifact: ${path}`);
}

function filesBelow(path) {
  const result = [];
  for (const name of readdirSync(path)) {
    const item = join(path, name);
    if (statSync(item).isDirectory()) result.push(...filesBelow(item));
    else result.push(item);
  }
  return result;
}

const markdownFiles = ['README.md', 'PLAN.md', 'TODO.md'].map((path) => join(root, path));
markdownFiles.push(...filesBelow(join(root, 'docs')).filter((path) => path.endsWith('.md')));
for (const file of markdownFiles) {
  const text = readFileSync(file, 'utf8');
  if (text.includes('\r')) errors.push(`CR character in ${file}`);
  for (const match of text.matchAll(/\[[^\]]*\]\(([^)]+)\)/g)) {
    let target = match[1].trim().replace(/^<|>$/g, '').split('#', 1)[0];
    if (!target || /^(https?:|mailto:)/.test(target) || target.startsWith('/')) continue;
    target = decodeURIComponent(target);
    const resolved = normalize(resolve(dirname(file), target));
    if (!resolved.startsWith(root) || !existsSync(resolved)) {
      errors.push(`broken local link in ${file}: ${match[1]}`);
    }
  }
}

const todo = readFileSync(join(root, 'TODO.md'), 'utf8');
for (let item = 1; item <= 22; item++) {
  const id = `GOV-${String(item).padStart(3, '0')}`;
  if (!todo.includes(`- [x] ${id}`)) errors.push(`${id} is not complete in TODO.md`);
}

const allContracts = required
  .filter((path) => path.endsWith('.md'))
  .map((path) => readFileSync(join(root, path), 'utf8'))
  .join('\n');
for (const invariant of [
  'fail closed',
  'caller',
  'credential',
  'authorization',
  'approval',
  'resource projection',
  'ambiguous',
  'evidence',
]) {
  if (!allContracts.toLowerCase().includes(invariant)) {
    errors.push(`security invariant is undocumented: ${invariant}`);
  }
}

const fixture = JSON.parse(readFileSync(join(root, 'docs/contracts/testdata/canonical-json-v1.json'), 'utf8'));
const domain = Buffer.from(fixture.domain, 'utf8');
for (const vector of fixture.vectors) {
  const canonical = Buffer.from(vector.canonical, 'utf8');
  if (canonical.toString('hex') !== vector.canonical_hex) {
    errors.push(`canonical hex mismatch: ${vector.name}`);
  }
  const digest = createHash('sha256').update(domain).update(canonical).digest('hex');
  if (digest !== vector.digest_hex) errors.push(`digest mismatch: ${vector.name}`);
}

const openapi = readFileSync(join(root, 'api/openapi.yaml'), 'utf8');
for (const marker of ['openapi: 3.1.2', '/v1/tools:', '/v1/tool-calls:', '/v1/admin/tool-versions:', '/livez:', '/readyz:', 'application/problem+json:']) {
  if (!openapi.includes(marker)) errors.push(`OpenAPI marker missing: ${marker}`);
}

if (errors.length) {
  for (const error of errors) console.error(`ERROR: ${error}`);
  process.exit(1);
}

console.log(`Phase 0 validation passed: ${required.length} artifacts, Markdown links, checklist, security invariants, canonical JSON vectors, and OpenAPI structure.`);
