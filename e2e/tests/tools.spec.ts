import { test, expect } from '@playwright/test';
import { OctroiAPI } from '../helpers/api.js';

let api: OctroiAPI;

test.beforeAll(async () => {
  api = await OctroiAPI.login('admin@octroi.dev', 'octroi');
});

test.describe('Tools tab', () => {
  test('table loads with seed data', async ({ page }) => {
    await page.goto('/ui#tools');

    await expect(page.locator('#tools-body tr').first()).toBeVisible();
    const rows = page.locator('#tools-body tr');
    await expect(rows).not.toHaveCount(0);
  });

  test('search filters table rows', async ({ page }) => {
    await page.goto('/ui#tools');
    await expect(page.locator('#tools-body tr').first()).toBeVisible();

    const totalRows = await page.locator('#tools-body tr').count();

    // Use the name of the first tool to search.
    const firstNameCell = page.locator('#tools-body tr td:first-child a').first();
    const firstName = await firstNameCell.innerText();
    await page.locator('#tools-search').fill(firstName);

    const filteredRows = await page.locator('#tools-body tr').count();
    expect(filteredRows).toBeGreaterThan(0);
    expect(filteredRows).toBeLessThanOrEqual(totalRows);

    // Clear search restores all rows.
    await page.locator('#tools-search').fill('');
    await expect(page.locator('#tools-body tr')).toHaveCount(totalRows);
  });

  test.describe('create tool (service mode)', () => {
    let createdToolId: string | undefined;

    test.afterEach(async () => {
      if (createdToolId) {
        await api.deleteTool(createdToolId);
        createdToolId = undefined;
      }
    });

    test('creates a service tool', async ({ page }) => {
      await page.goto('/ui#tools');
      await expect(page.locator('#tools-body tr').first()).toBeVisible();

      const toolName = `E2E Tool ${Date.now()}`;

      // Open the create modal.
      await page.locator('#tools-create-btn').click();
      await expect(page.locator('#tool-modal')).toHaveClass(/open/);

      // Select service mode.
      await page.locator('#tool-mode').selectOption('service');

      // Fill out the form.
      await page.locator('#tool-name').fill(toolName);
      await page.locator('#tool-description').fill('Tool created by e2e test');
      await page.locator('#tool-endpoint').fill('https://httpbin.org/get');
      await page.locator('#tool-auth-type').selectOption('none');

      // Submit.
      await page.locator('#tool-submit-btn').click();

      // Modal should close and the new row should appear.
      await expect(page.locator('#tool-modal')).not.toHaveClass(/open/);
      const row = page.locator('#tools-body tr', { hasText: toolName });
      await expect(row).toBeVisible();

      // Extract id from onclick attribute of the Edit button for cleanup.
      const editBtn = row.locator('button', { hasText: 'Edit' });
      const onclick = await editBtn.getAttribute('onclick');
      const match = onclick?.match(/editTool\('([^']+)'\)/);
      if (match) createdToolId = match[1];
    });
  });

  test.describe('edit tool', () => {
    let toolId: string;

    test.beforeEach(async () => {
      const result = await api.createTool({
        name: 'E2E Edit Tool',
        description: 'Original description',
        mode: 'service',
        endpoint: 'https://httpbin.org/get',
        auth_type: 'none',
      });
      toolId = result.id;
    });

    test.afterEach(async () => {
      if (toolId) await api.deleteTool(toolId);
    });

    test('edits tool description', async ({ page }) => {
      await page.goto('/ui#tools');
      await expect(page.locator('#tools-body tr').first()).toBeVisible();

      const row = page.locator('#tools-body tr', { hasText: 'E2E Edit Tool' });
      await expect(row).toBeVisible();

      // Click Edit.
      await row.locator('button', { hasText: 'Edit' }).click();
      await expect(page.locator('#tool-modal')).toHaveClass(/open/);
      await expect(page.locator('#tool-modal-title')).toHaveText('Edit Tool');

      // Change description.
      await page.locator('#tool-description').fill('Updated description');
      await page.locator('#tool-submit-btn').click();

      // Wait for modal to close and verify the row updated.
      await expect(page.locator('#tool-modal')).not.toHaveClass(/open/);
      await expect(
        page.locator('#tools-body tr', { hasText: 'Updated description' }),
      ).toBeVisible();
    });
  });

  test.describe('delete tool', () => {
    let toolId: string;
    let deleted = false;

    test.beforeEach(async () => {
      const result = await api.createTool({
        name: 'E2E Delete Tool',
        description: 'Will be deleted',
        mode: 'service',
        endpoint: 'https://httpbin.org/get',
        auth_type: 'none',
      });
      toolId = result.id;
      deleted = false;
    });

    test.afterEach(async () => {
      if (!deleted && toolId) await api.deleteTool(toolId);
    });

    test('deletes tool via confirm modal', async ({ page }) => {
      await page.goto('/ui#tools');
      await expect(page.locator('#tools-body tr').first()).toBeVisible();

      const row = page.locator('#tools-body tr', { hasText: 'E2E Delete Tool' });
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
