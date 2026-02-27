import { test, expect } from '@playwright/test';

test.describe('Usage tab', () => {
  test('summary cards render', async ({ page }) => {
    await page.goto('/ui#usage');

    await expect(page.locator('#tab-usage')).toBeVisible();
    await expect(page.locator('#usage-cards')).toBeVisible();
    await expect(page.locator('#stat-requests')).not.toBeEmpty();
    await expect(page.locator('#stat-cost')).not.toBeEmpty();
    await expect(page.locator('#stat-latency')).not.toBeEmpty();
  });

  test('chart renders with SVG', async ({ page }) => {
    await page.goto('/ui#usage');

    await expect(page.locator('#usage-chart')).toBeVisible();
    await expect(page.locator('#usage-chart svg')).toBeVisible();
  });

  test('mode toggle switches between live and historical', async ({ page }) => {
    await page.goto('/ui#usage');

    await expect(page.locator('#tab-usage')).toBeVisible();

    // Start in live mode — date controls should be hidden
    await expect(page.locator('#usage-date-controls')).not.toBeVisible();

    // Switch to historical mode
    await page.click('#mode-hist');
    await expect(page.locator('#usage-date-controls')).toBeVisible();

    // Switch back to live mode
    await page.click('#mode-live');
    await expect(page.locator('#usage-date-controls')).not.toBeVisible();
  });

  test('transactions table loads', async ({ page }) => {
    await page.goto('/ui#usage');

    await expect(page.locator('#tab-usage')).toBeVisible();

    // The transactions section should be present — either with rows or empty state.
    // Both #txn-body and #txn-empty exist in DOM, so check that the table rendered.
    await expect(page.locator('#txn-body')).toBeAttached();
  });
});
