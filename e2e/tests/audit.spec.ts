import { test, expect } from '@playwright/test';

test.describe('Audit log tab', () => {
  test('audit entries load', async ({ page }) => {
    await page.goto('/ui#audit');

    await expect(page.locator('#tab-audit')).toBeVisible();

    const rows = page.locator('#audit-table-body tr');
    await expect(rows.first()).toBeVisible();
    expect(await rows.count()).toBeGreaterThanOrEqual(1);
  });

  test('resource type filter', async ({ page }) => {
    await page.goto('/ui#audit');

    await expect(page.locator('#audit-table-body tr').first()).toBeVisible();

    await page.locator('#audit-resource-type').selectOption('user');

    // Wait for the table to refresh after filter change
    await page.waitForResponse((resp) =>
      resp.url().includes('/api/v1/admin/audit-log') && resp.ok(),
    );

    // Table should still render without errors
    await expect(page.locator('#tab-audit')).toBeVisible();
    await expect(page.locator('#audit-table-body')).toBeVisible();
  });
});
