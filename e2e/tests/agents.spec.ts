import { test, expect } from '@playwright/test';
import { OctroiAPI } from '../helpers/api.js';

let api: OctroiAPI;

test.beforeAll(async () => {
  api = await OctroiAPI.login('admin@octroi.dev', 'octroi');
});

test.describe('Agents tab', () => {
  test('table loads with seed data', async ({ page }) => {
    await page.goto('/ui#agents');

    await expect(page.locator('#agents-body tr').first()).toBeVisible();
    const rows = page.locator('#agents-body tr');
    await expect(rows).not.toHaveCount(0);
  });

  test('search filters table rows', async ({ page }) => {
    await page.goto('/ui#agents');
    await expect(page.locator('#agents-body tr').first()).toBeVisible();

    const totalRows = await page.locator('#agents-body tr').count();

    // Use the name of the first agent so at least one row matches.
    const firstName = await page.locator('#agents-body tr td:first-child').first().innerText();
    await page.locator('#agents-search').fill(firstName);

    const filteredRows = await page.locator('#agents-body tr').count();
    expect(filteredRows).toBeGreaterThan(0);
    expect(filteredRows).toBeLessThanOrEqual(totalRows);

    // Clear search restores all rows.
    await page.locator('#agents-search').fill('');
    await expect(page.locator('#agents-body tr')).toHaveCount(totalRows);
  });

  test.describe('create agent', () => {
    let createdAgentId: string | undefined;

    test.afterEach(async () => {
      if (createdAgentId) {
        await api.deleteAgent(createdAgentId);
        createdAgentId = undefined;
      }
    });

    test('creates agent and shows API key', async ({ page }) => {
      await page.goto('/ui#agents');
      await expect(page.locator('#agents-body tr').first()).toBeVisible();

      const agentName = `E2E Agent ${Date.now()}`;

      // Open the create modal.
      await page.locator('button', { hasText: '+ Create Agent' }).click();
      await expect(page.locator('#agent-modal')).toHaveClass(/open/);

      // Fill out the form.
      await page.locator('#agent-name').fill(agentName);

      // Select the first available team.
      const teamSelect = page.locator('#agent-team');
      await teamSelect.selectOption({ index: 1 });

      // Submit.
      await page.locator('#agent-submit-btn').click();

      // Verify the API key banner appears with an octroi_ prefixed key.
      await expect(page.locator('#agent-key-banner')).toHaveClass(/show/);
      const keyValue = await page.locator('#agent-key-value').innerText();
      expect(keyValue).toMatch(/^octroi_/);

      // Navigate back to agents tab to find the row.
      await page.goto('/ui#agents');
      await expect(page.locator('#agents-body tr').first()).toBeVisible();

      const row = page.locator('#agents-body tr', { hasText: agentName });
      await expect(row).toBeVisible();

      // Extract id from onclick attribute of the Edit button for cleanup.
      const editBtn = row.locator('button', { hasText: 'Edit' });
      const onclick = await editBtn.getAttribute('onclick');
      const match = onclick?.match(/editAgent\('([^']+)'\)/);
      if (match) createdAgentId = match[1];
    });
  });

  test.describe('edit agent', () => {
    let agentId: string;

    test.beforeEach(async () => {
      const teams = await api.listTeams();
      const teamName = teams[0]?.name || 'platform';
      const result = await api.createAgent({ name: 'E2E Edit Agent', team: teamName });
      agentId = result.id;
    });

    test.afterEach(async () => {
      if (agentId) await api.deleteAgent(agentId);
    });

    test('edits agent name', async ({ page }) => {
      await page.goto('/ui#agents');
      await expect(page.locator('#agents-body tr').first()).toBeVisible();

      const row = page.locator('#agents-body tr', { hasText: 'E2E Edit Agent' });
      await expect(row).toBeVisible();

      // Click Edit.
      await row.locator('button', { hasText: 'Edit' }).click();
      await expect(page.locator('#agent-modal')).toHaveClass(/open/);
      await expect(page.locator('#agent-modal-title')).toHaveText('Edit Agent');

      // Change name.
      await page.locator('#agent-name').fill('E2E Edited Agent');
      await page.locator('#agent-submit-btn').click();

      // Wait for modal to close and verify the row updated.
      await expect(page.locator('#agent-modal')).not.toHaveClass(/open/);
      await expect(page.locator('#agents-body tr', { hasText: 'E2E Edited Agent' })).toBeVisible();
    });
  });

  test.describe('delete agent', () => {
    let agentId: string;
    let deleted = false;

    test.beforeEach(async () => {
      const teams = await api.listTeams();
      const teamName = teams[0]?.name || 'platform';
      const result = await api.createAgent({ name: 'E2E Delete Agent', team: teamName });
      agentId = result.id;
      deleted = false;
    });

    test.afterEach(async () => {
      if (!deleted && agentId) await api.deleteAgent(agentId);
    });

    test('deletes agent via confirm modal', async ({ page }) => {
      await page.goto('/ui#agents');
      await expect(page.locator('#agents-body tr').first()).toBeVisible();

      const row = page.locator('#agents-body tr', { hasText: 'E2E Delete Agent' });
      await expect(row).toBeVisible();

      // Click Delete.
      await row.locator('button', { hasText: 'Delete' }).click();

      // Confirm modal should appear.
      await expect(page.locator('#confirm-modal')).toHaveClass(/open/);
      await page.locator('#confirm-ok').click();

      // Row should be removed.
      await expect(row).not.toBeVisible();
      deleted = true;
    });
  });
});
