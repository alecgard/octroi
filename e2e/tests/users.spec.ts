import { test, expect } from '@playwright/test';
import { OctroiAPI } from '../helpers/api.js';

let api: OctroiAPI;

test.beforeAll(async () => {
  api = await OctroiAPI.login('admin@octroi.dev', 'octroi');
});

test.describe('Users tab', () => {
  test('table loads with existing users', async ({ page }) => {
    await page.goto('/ui#users');

    await expect(page.locator('#tab-users')).toBeVisible();
    await expect(page.locator('#users-body tr').first()).toBeVisible();
  });

  test.describe('create user', () => {
    const testEmail = `e2e-create-${Date.now()}@test.com`;

    test.afterEach(async ({ page }) => {
      // Find and delete the created user via API
      const row = page.locator('#users-body tr', { hasText: testEmail });
      const userId = await row.getAttribute('data-id').catch(() => null);
      if (userId) {
        await api.deleteUser(userId);
      }
    });

    test('create user via modal', async ({ page }) => {
      await page.goto('/ui#users');
      await expect(page.locator('#tab-users')).toBeVisible();

      await page.click('#users-create-btn');
      await expect(page.locator('#user-modal')).toBeVisible();

      await page.fill('#user-email', testEmail);
      await page.fill('#user-password', 'testpass123');
      await page.fill('#user-name-input', 'E2E User');
      await page.locator('#user-role').selectOption('member');

      await page.click('#user-submit-btn');

      await expect(
        page.locator('#users-body tr', { hasText: testEmail }),
      ).toBeVisible();
    });
  });

  test.describe('edit user', () => {
    let userId: string;
    const testEmail = `e2e-edit-${Date.now()}@test.com`;

    test.beforeEach(async () => {
      const user = await api.createUser({
        email: testEmail,
        password: 'testpass123',
        name: 'E2E Edit Before',
        role: 'member',
      });
      userId = user.id;
    });

    test.afterEach(async () => {
      await api.deleteUser(userId);
    });

    test('edit user via modal', async ({ page }) => {
      await page.goto('/ui#users');
      await expect(page.locator('#tab-users')).toBeVisible();

      const row = page.locator('#users-body tr', { hasText: testEmail });
      await expect(row).toBeVisible();
      await row.getByRole('button', { name: /edit/i }).click();

      await expect(page.locator('#user-modal')).toBeVisible();
      await page.fill('#user-name-input', 'E2E Edit After');
      await page.click('#user-submit-btn');

      await expect(
        page.locator('#users-body tr', { hasText: 'E2E Edit After' }),
      ).toBeVisible();
    });
  });

  test.describe('delete user', () => {
    const testEmail = `e2e-delete-${Date.now()}@test.com`;

    test.beforeEach(async () => {
      await api.createUser({
        email: testEmail,
        password: 'testpass123',
        name: 'E2E Delete Me',
        role: 'member',
      });
    });

    test('delete user via confirm modal', async ({ page }) => {
      await page.goto('/ui#users');
      await expect(page.locator('#tab-users')).toBeVisible();

      const row = page.locator('#users-body tr', { hasText: testEmail });
      await expect(row).toBeVisible();
      await row.getByRole('button', { name: /delete/i }).click();

      // Confirm deletion in the confirm modal
      await expect(page.locator('#confirm-modal')).toHaveClass(/open/);
      await page.click('#confirm-ok');

      await expect(row).not.toBeVisible();
    });
  });
});
