import { defineConfig, devices } from '@playwright/test';

export default defineConfig({
  testDir: './tests',
  fullyParallel: false,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 1 : 0,
  workers: 1,
  reporter: process.env.CI
    ? [['github'], ['html', { open: 'never' }]]
    : [['html', { open: 'on-failure' }]],
  use: {
    baseURL: process.env.BASE_URL || 'http://local.localhost:8080',
    trace: 'on-first-retry',
    screenshot: 'only-on-failure',
    video: 'on-first-retry',
  },
  globalSetup: './global-setup.ts',
  projects: [
    {
      name: 'admin',
      use: {
        ...devices['Desktop Chrome'],
        storageState: './auth/admin.json',
      },
      testIgnore: /role-guards/,
    },
    {
      name: 'member',
      use: {
        ...devices['Desktop Chrome'],
        storageState: './auth/member.json',
      },
      testMatch: /role-guards/,
    },
  ],
});
