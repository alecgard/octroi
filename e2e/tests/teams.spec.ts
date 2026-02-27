import { test, expect } from '@playwright/test';

test.describe('Teams tab', () => {
  test('team cards render', async ({ page }) => {
    await page.goto('/ui#teams');

    await expect(page.locator('#tab-teams')).toBeVisible();
    await expect(page.locator('#teams-content .team-section').first()).toBeVisible();
  });

  test('search filters team cards', async ({ page }) => {
    await page.goto('/ui#teams');

    await expect(page.locator('#tab-teams')).toBeVisible();
    await expect(page.locator('#teams-content .team-section').first()).toBeVisible();

    const countBefore = await page.locator('#teams-content .team-section').count();
    expect(countBefore).toBeGreaterThan(0);

    // Type a search term that won't match any teams.
    await page.fill('#teams-search', 'zzz-no-match');

    // Cards are removed from DOM via innerHTML replacement, so count should drop to 0.
    await expect(page.locator('#teams-content .team-section')).toHaveCount(0);

    // Clear search restores all cards.
    await page.fill('#teams-search', '');
    await expect(page.locator('#teams-content .team-section')).toHaveCount(countBefore);
  });

  test('create team via modal', async ({ page }) => {
    await page.goto('/ui#teams');

    await expect(page.locator('#tab-teams')).toBeVisible();

    const teamName = `e2e-test-${Date.now()}`;

    await page.click('#teams-create-btn');
    await expect(page.locator('#create-team-modal')).toBeVisible();

    await page.fill('#create-team-name', teamName);

    // Select the first available admin from the dropdown
    const adminSelect = page.locator('#create-team-admin');
    const options = adminSelect.locator('option');
    const optionCount = await options.count();
    // Pick the first non-placeholder option
    for (let i = 0; i < optionCount; i++) {
      const val = await options.nth(i).getAttribute('value');
      if (val) {
        await adminSelect.selectOption(val);
        break;
      }
    }

    await page.click('#create-team-modal button.btn-primary');

    // Verify the new team card appears
    await expect(
      page.locator('#teams-content .team-section', { hasText: teamName }),
    ).toBeVisible();
  });
});
