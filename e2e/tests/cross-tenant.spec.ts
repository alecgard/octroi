import { test, expect } from '@playwright/test';
import { OctroiAPI, serverURL } from '../helpers/api.js';

// Cross-tenant isolation tests.
// The "local" tenant is seeded with data (agents, tools, users, teams).
// The "rival" tenant is created by ensure-admin with only an admin user.
// These tests verify that data from one tenant never leaks to the other.

const LOCAL_SLUG = 'local';
const RIVAL_SLUG = 'rival';

let localAPI: OctroiAPI;
let rivalAPI: OctroiAPI;

test.beforeAll(async () => {
  localAPI = await OctroiAPI.login('admin@octroi.dev', 'octroi', LOCAL_SLUG);
  rivalAPI = await OctroiAPI.login('admin@octroi.dev', 'octroi', RIVAL_SLUG);
});

test.describe('Cross-tenant isolation', () => {
  test('rival tenant cannot see local tenant agents', async () => {
    const localAgents = await localAPI.listAgents();
    const rivalAgents = await rivalAPI.listAgents();

    expect(localAgents.agents.length).toBeGreaterThan(0);
    expect(rivalAgents.agents ?? []).toHaveLength(0);
  });

  test('rival tenant cannot see local tenant tools', async () => {
    const localTools = await localAPI.listTools();
    const rivalTools = await rivalAPI.listTools();

    expect(localTools.tools.length).toBeGreaterThan(0);
    expect(rivalTools.tools ?? []).toHaveLength(0);
  });

  test('rival tenant cannot see local tenant users (beyond its own admin)', async () => {
    const localUsers = await localAPI.listUsers();
    const rivalUsers = await rivalAPI.listUsers();

    // Local tenant has seeded users + the e2e member user.
    expect(localUsers.users.length).toBeGreaterThan(1);
    // Rival tenant only has the admin created by ensure-admin.
    expect(rivalUsers.users.length).toBe(1);
  });

  test('rival tenant cannot see local tenant usage', async () => {
    const rivalUsage = (await rivalAPI.getUsage()) as { total_requests: number };
    expect(rivalUsage.total_requests).toBe(0);
  });

  test('agent created in rival is not visible in local', async () => {
    const agent = await rivalAPI.createAgent({ name: 'Rival Agent', team: 'platform' });

    try {
      // Verify it appears in rival.
      const rivalAgents = await rivalAPI.listAgents();
      const found = (rivalAgents.agents ?? []).some(
        (a: Record<string, unknown>) => a.id === agent.id,
      );
      expect(found).toBe(true);

      // Verify it does NOT appear in local.
      const localAgents = await localAPI.listAgents();
      const leaked = localAgents.agents.some(
        (a: Record<string, unknown>) => a.id === agent.id,
      );
      expect(leaked).toBe(false);
    } finally {
      await rivalAPI.deleteAgent(agent.id);
    }
  });

  test('tool created in rival is not visible in local', async () => {
    const tool = await rivalAPI.createTool({
      name: 'Rival Tool',
      description: 'A tool only visible in the rival tenant',
      endpoint: 'https://rival.example.com/api',
      mode: 'api',
    });

    try {
      const rivalTools = await rivalAPI.listTools();
      const found = (rivalTools.tools ?? []).some(
        (t: Record<string, unknown>) => t.id === tool.id,
      );
      expect(found).toBe(true);

      const localTools = await localAPI.listTools();
      const leaked = localTools.tools.some(
        (t: Record<string, unknown>) => t.id === tool.id,
      );
      expect(leaked).toBe(false);

      // Public tool endpoint also scoped by tenant.
      const resp = await localAPI.rawGet(`/api/v1/tools/${tool.id}`);
      expect(resp.status).toBe(404);
    } finally {
      await rivalAPI.deleteTool(tool.id);
    }
  });

  test('user created in rival cannot log into local tenant', async () => {
    const user = await rivalAPI.createUser({
      email: `rival-only-${Date.now()}@example.com`,
      password: 'testpassword123',
      name: 'Rival User',
      role: 'member',
      teams: [],
    });

    try {
      // Verify rival user cannot log into local tenant.
      const loginResp = await fetch(`${serverURL}/api/v1/auth/login`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', 'X-Tenant-Slug': LOCAL_SLUG },
        body: JSON.stringify({ email: user.email, password: 'testpassword123' }),
      });
      expect(loginResp.status).toBe(401);
    } finally {
      await rivalAPI.deleteUser(user.id);
    }
  });

  test('rival tenant agent API key rejected on local tenant', async () => {
    const agent = await rivalAPI.createAgent({ name: 'Rival Key Agent', team: 'platform' });

    try {
      const apiKey = agent.api_key as string;
      expect(apiKey).toMatch(/^octroi_/);

      // Try using this agent key against the local tenant.
      const resp = await fetch(`${serverURL}/api/v1/agents/me`, {
        headers: {
          Authorization: `Bearer ${apiKey}`,
          'X-Tenant-Slug': LOCAL_SLUG,
        },
      });
      expect(resp.status).toBe(403);
    } finally {
      await rivalAPI.deleteAgent(agent.id);
    }
  });

  test('rival agent cannot proxy to local tenant tool', async () => {
    // Get a tool ID from the local tenant (seeded data).
    const localTools = await localAPI.listTools();
    const localToolId = localTools.tools[0].id as string;

    // Create an agent in the rival tenant.
    const agent = await rivalAPI.createAgent({ name: 'Rival Proxy Agent', team: 'platform' });

    try {
      const apiKey = agent.api_key as string;

      // Use the rival agent key on the rival tenant subdomain,
      // but try to proxy to a tool ID that belongs to the local tenant.
      const resp = await fetch(`${serverURL}/proxy/${localToolId}/anything`, {
        headers: {
          Authorization: `Bearer ${apiKey}`,
          'X-Tenant-Slug': RIVAL_SLUG,
        },
      });

      // The proxy looks up the tool by (tenantID, toolID). Since this tool
      // belongs to the local tenant, it won't be found in the rival tenant.
      expect(resp.status).toBe(404);
    } finally {
      await rivalAPI.deleteAgent(agent.id);
    }
  });
});
