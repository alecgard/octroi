import { test, expect } from '@playwright/test';

// These tests exercise the login flow itself, so we must NOT use stored auth state.
test.use({ storageState: { cookies: [], origins: [] } });

test.describe('Login flow', () => {
  test('successful login shows tab bar and user name', async ({ page }) => {
    await page.goto('/ui');
    await expect(page.locator('#login-screen')).toBeVisible();

    await page.fill('#login-email', 'admin@octroi.dev');
    await page.fill('#login-password', 'octroi');
    await page.click('#login-btn');

    await expect(page.locator('#tab-bar')).toBeVisible();
    await expect(page.locator('#topbar-user-name')).not.toBeEmpty();
  });

  test('failed login shows error and tab bar stays hidden', async ({ page }) => {
    await page.goto('/ui');
    await expect(page.locator('#login-screen')).toBeVisible();

    await page.fill('#login-email', 'admin@octroi.dev');
    await page.fill('#login-password', 'wrong-password');
    await page.click('#login-btn');

    await expect(page.locator('#login-error')).not.toBeEmpty();
    await expect(page.locator('#tab-bar')).not.toBeVisible();
  });

  test('logout returns to login screen', async ({ page }) => {
    await page.goto('/ui');

    // Login first
    await page.fill('#login-email', 'admin@octroi.dev');
    await page.fill('#login-password', 'octroi');
    await page.click('#login-btn');
    await expect(page.locator('#tab-bar')).toBeVisible();

    // Click logout
    await page.locator('#user-info').getByText('Logout').click();

    await expect(page.locator('#login-screen')).toBeVisible();
  });
});
