import { test, expect } from '@playwright/test';

test.describe('Tab navigation', () => {
  test('default tab is agents', async ({ page }) => {
    await page.goto('/ui');

    await expect(page.locator('button[data-tab="agents"]')).toHaveClass(/active/);
    await expect(page.locator('#tab-agents')).toBeVisible();
  });

  test('hash routing to tools tab', async ({ page }) => {
    await page.goto('/ui#tools');

    await expect(page.locator('button[data-tab="tools"]')).toHaveClass(/active/);
    await expect(page.locator('#tab-tools')).toBeVisible();
  });

  test('deep link to usage tab', async ({ page }) => {
    await page.goto('/ui#usage');

    await expect(page.locator('button[data-tab="usage"]')).toHaveClass(/active/);
    await expect(page.locator('#tab-usage')).toBeVisible();
  });
});
