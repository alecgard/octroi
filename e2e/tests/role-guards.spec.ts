import { test, expect } from '@playwright/test';

test.describe('Role guards for member user', () => {
  test('metrics tab is disabled', async ({ page }) => {
    await page.goto('/ui');

    await expect(page.locator('#metrics-tab-btn')).toBeDisabled();
  });

  test('create tool button is disabled', async ({ page }) => {
    await page.goto('/ui#tools');

    await expect(page.locator('#tools-create-btn')).toBeDisabled();
  });

  test('create user button is disabled', async ({ page }) => {
    await page.goto('/ui#users');

    await expect(page.locator('#users-create-btn')).toBeDisabled();
  });

  test('create team button is disabled', async ({ page }) => {
    await page.goto('/ui#teams');

    await expect(page.locator('#teams-create-btn')).toBeDisabled();
  });
});
