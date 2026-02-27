import { chromium, type FullConfig } from '@playwright/test';
import fs from 'fs';
import path from 'path';

const BASE_URL = process.env.BASE_URL || 'http://local.localhost:8080';
const ADMIN_EMAIL = 'admin@octroi.dev';
const ADMIN_PASSWORD = 'octroi';
const MEMBER_EMAIL = 'e2e-member@octroi.dev';
const MEMBER_PASSWORD = 'octroi';

// Derive the raw server URL (localhost) and tenant slug from BASE_URL.
// Node.js fetch can't resolve *.localhost subdomains, so we connect to
// localhost directly and set X-Tenant-Slug for tenant routing.
function deriveServerURL(url: string): { serverURL: string; tenantSlug: string } {
  const parsed = new URL(url);
  const parts = parsed.hostname.split('.');
  const tenantSlug = parts.length > 1 ? parts[0] : '';
  parsed.hostname = 'localhost';
  return { serverURL: parsed.toString().replace(/\/$/, ''), tenantSlug };
}

const { serverURL, tenantSlug } = deriveServerURL(BASE_URL);

async function waitForServer(timeoutMs = 30000) {
  const start = Date.now();
  while (Date.now() - start < timeoutMs) {
    try {
      const resp = await fetch(`${serverURL}/health`);
      if (resp.ok) return;
    } catch {
      // server not ready yet
    }
    await new Promise((r) => setTimeout(r, 1000));
  }
  throw new Error(`Server not ready at ${serverURL} after ${timeoutMs}ms`);
}

async function apiLogin(email: string, password: string) {
  const resp = await fetch(`${serverURL}/api/v1/auth/login`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', 'X-Tenant-Slug': tenantSlug },
    body: JSON.stringify({ email, password }),
  });
  if (!resp.ok) throw new Error(`Login failed for ${email}: ${resp.status}`);
  return resp.json() as Promise<{ token: string; user: Record<string, unknown> }>;
}

async function loginAndSave(email: string, password: string, statePath: string) {
  const { token, user } = await apiLogin(email, password);

  const browser = await chromium.launch();
  const context = await browser.newContext();
  const page = await context.newPage();

  // Playwright browser resolves *.localhost natively.
  await page.goto(`${BASE_URL}/ui`);
  await page.evaluate(
    ({ token, user }) => {
      localStorage.setItem('octroi_session_token', token);
      localStorage.setItem('octroi_user', JSON.stringify(user));
    },
    { token, user },
  );

  const dir = path.dirname(statePath);
  if (!fs.existsSync(dir)) fs.mkdirSync(dir, { recursive: true });
  await context.storageState({ path: statePath });
  await browser.close();
}

async function ensureMemberUser() {
  const { token } = await apiLogin(ADMIN_EMAIL, ADMIN_PASSWORD);

  // Create member user (ignore if already exists).
  await fetch(`${serverURL}/api/v1/admin/users`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      Authorization: `Bearer ${token}`,
      'X-Tenant-Slug': tenantSlug,
    },
    body: JSON.stringify({
      email: MEMBER_EMAIL,
      password: MEMBER_PASSWORD,
      name: 'E2E Member',
      role: 'member',
      teams: [{ team: 'platform', role: 'member' }],
    }),
  });
}

export default async function globalSetup(_config: FullConfig) {
  await waitForServer();
  await ensureMemberUser();

  const authDir = path.resolve(__dirname, 'auth');
  await loginAndSave(ADMIN_EMAIL, ADMIN_PASSWORD, path.join(authDir, 'admin.json'));
  await loginAndSave(MEMBER_EMAIL, MEMBER_PASSWORD, path.join(authDir, 'member.json'));
}
