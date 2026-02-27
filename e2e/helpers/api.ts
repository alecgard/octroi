const BASE_URL = process.env.BASE_URL || 'http://local.localhost:8080';

// Node.js fetch can't resolve *.localhost subdomains.
// Derive a raw localhost URL and tenant slug for programmatic calls.
function deriveServerURL(url: string): { serverURL: string; tenantSlug: string } {
  const parsed = new URL(url);
  const parts = parsed.hostname.split('.');
  const tenantSlug = parts.length > 1 ? parts[0] : '';
  parsed.hostname = 'localhost';
  return { serverURL: parsed.toString().replace(/\/$/, ''), tenantSlug };
}

const { serverURL, tenantSlug } = deriveServerURL(BASE_URL);

export class OctroiAPI {
  private token: string;

  constructor(token: string) {
    this.token = token;
  }

  /**
   * Login via native fetch (no Playwright fixture dependency).
   * Safe to call from beforeAll, beforeEach, or afterEach.
   */
  static async login(email: string, password: string) {
    const resp = await fetch(`${serverURL}/api/v1/auth/login`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'X-Tenant-Slug': tenantSlug },
      body: JSON.stringify({ email, password }),
    });
    if (!resp.ok) throw new Error(`Login failed: ${resp.status} ${await resp.text()}`);
    const body = (await resp.json()) as { token: string };
    return new OctroiAPI(body.token);
  }

  private headers() {
    return {
      Authorization: `Bearer ${this.token}`,
      'Content-Type': 'application/json',
      'X-Tenant-Slug': tenantSlug,
    };
  }

  async createAgent(body: Record<string, unknown>) {
    const resp = await fetch(`${serverURL}/api/v1/admin/agents`, {
      method: 'POST',
      headers: this.headers(),
      body: JSON.stringify(body),
    });
    if (!resp.ok) throw new Error(`createAgent failed: ${resp.status} ${await resp.text()}`);
    return resp.json();
  }

  async deleteAgent(id: string) {
    await fetch(`${serverURL}/api/v1/admin/agents/${id}`, {
      method: 'DELETE',
      headers: this.headers(),
    });
  }

  async createTool(body: Record<string, unknown>) {
    const resp = await fetch(`${serverURL}/api/v1/admin/tools`, {
      method: 'POST',
      headers: this.headers(),
      body: JSON.stringify(body),
    });
    if (!resp.ok) throw new Error(`createTool failed: ${resp.status} ${await resp.text()}`);
    return resp.json();
  }

  async deleteTool(id: string) {
    await fetch(`${serverURL}/api/v1/admin/tools/${id}`, {
      method: 'DELETE',
      headers: this.headers(),
    });
  }

  async createUser(body: Record<string, unknown>) {
    const resp = await fetch(`${serverURL}/api/v1/admin/users`, {
      method: 'POST',
      headers: this.headers(),
      body: JSON.stringify(body),
    });
    if (!resp.ok) throw new Error(`createUser failed: ${resp.status} ${await resp.text()}`);
    return resp.json();
  }

  async deleteUser(id: string) {
    await fetch(`${serverURL}/api/v1/admin/users/${id}`, {
      method: 'DELETE',
      headers: this.headers(),
    });
  }

  async listTeams() {
    const resp = await fetch(`${serverURL}/api/v1/admin/teams`, {
      headers: this.headers(),
    });
    return resp.json();
  }
}
