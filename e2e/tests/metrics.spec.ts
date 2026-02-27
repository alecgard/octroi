import { test, expect } from '@playwright/test';

test.describe('Metrics tab', () => {
  test('metrics tab loads', async ({ page }) => {
    await page.goto('/ui#metrics');

    await expect(page.locator('#tab-metrics')).toBeVisible();
    await expect(page.locator('#metrics-cards').locator('> *').first()).toBeVisible();
  });

  test('metric cards have content', async ({ page }) => {
    await page.goto('/ui#metrics');

    await expect(page.locator('#metrics-cards').locator('> *').first()).toBeVisible();

    const firstCard = page.locator('#metrics-cards').locator('> *').first();
    await expect(firstCard).not.toBeEmpty();
    const text = await firstCard.textContent();
    expect(text!.trim().length).toBeGreaterThan(0);
  });
});
