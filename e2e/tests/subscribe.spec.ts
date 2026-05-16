import { test, expect } from "@playwright/test";

// uniqueEmail keeps each test isolated against a shared DB without
// teardown — every run gets a fresh address.
function uniqueEmail(prefix: string): string {
  const suffix = `${Date.now()}-${Math.random().toString(36).slice(2, 8)}`;
  return `${prefix}+${suffix}@example.com`;
}

test.describe("subscribe page", () => {
  test.beforeEach(async ({ page }) => {
    await page.goto("/");
  });

  test("renders the form with all expected fields", async ({ page }) => {
    await expect(page).toHaveTitle(/Subscribe to GitHub release notifications/i);
    await expect(page.getByRole("heading", { name: /Subscribe to GitHub release notifications/i })).toBeVisible();
    await expect(page.locator("#email")).toBeVisible();
    await expect(page.locator("#repo")).toBeVisible();
    await expect(page.locator("#apikey")).toBeVisible();
    await expect(page.getByRole("button", { name: /subscribe/i })).toBeVisible();
  });

  test("successful submission shows confirmation message and clears form", async ({ page }) => {
    const email = uniqueEmail("happy");
    await page.locator("#email").fill(email);
    await page.locator("#repo").fill("golang/go");
    await page.getByRole("button", { name: /subscribe/i }).click();

    const status = page.locator("#status");
    await expect(status).toHaveClass(/status ok/);
    await expect(status).toContainText(/check your inbox/i);

    // Form is reset after success.
    await expect(page.locator("#email")).toHaveValue("");
    await expect(page.locator("#repo")).toHaveValue("");
  });

  test("duplicate subscription surfaces server-side conflict error", async ({ page }) => {
    const email = uniqueEmail("dup");

    // First submission — wait for the actual /api/subscribe response so
    // the second one cannot race past reset() or the disabled button.
    await page.locator("#email").fill(email);
    await page.locator("#repo").fill("kubernetes/kubernetes");
    const first = page.waitForResponse((r) => r.url().endsWith("/api/subscribe") && r.request().method() === "POST");
    await page.getByRole("button", { name: /subscribe/i }).click();
    expect((await first).status()).toBe(200);
    await expect(page.locator("#status")).toContainText(/check your inbox/i);

    // Second submission — same payload → 409.
    await page.locator("#email").fill(email);
    await page.locator("#repo").fill("kubernetes/kubernetes");
    const second = page.waitForResponse((r) => r.url().endsWith("/api/subscribe") && r.request().method() === "POST");
    await page.getByRole("button", { name: /subscribe/i }).click();
    expect((await second).status()).toBe(409);

    const status = page.locator("#status");
    await expect(status).toHaveClass(/status err/);
    await expect(status).toContainText(/already subscribed/i);
  });

  test("repo not found on GitHub renders error", async ({ page }) => {
    // The e2e-server stub maps owner "ghost" to ErrRepoNotFound.
    await page.locator("#email").fill(uniqueEmail("notfound"));
    await page.locator("#repo").fill("ghost/missing");
    await page.getByRole("button", { name: /subscribe/i }).click();

    const status = page.locator("#status");
    await expect(status).toHaveClass(/status err/);
    await expect(status).toContainText(/not found/i);
  });

  test("HTML5 validation blocks submit on malformed repo", async ({ page }) => {
    await page.locator("#email").fill(uniqueEmail("badrepo"));
    await page.locator("#repo").fill("no-slash-here");
    await page.getByRole("button", { name: /subscribe/i }).click();

    // The form's pattern attribute should prevent the request from
    // ever being sent; the status div stays hidden (display:none).
    const status = page.locator("#status");
    await expect(status).toHaveCSS("display", "none");
  });

  test("HTML5 validation blocks submit on invalid email", async ({ page }) => {
    await page.locator("#email").fill("not-an-email");
    await page.locator("#repo").fill("golang/go");
    await page.getByRole("button", { name: /subscribe/i }).click();

    const status = page.locator("#status");
    await expect(status).toHaveCSS("display", "none");
  });

  test("network failure shows graceful error", async ({ page, context }) => {
    await context.route("**/api/subscribe", (route) => route.abort("failed"));

    await page.locator("#email").fill(uniqueEmail("net"));
    await page.locator("#repo").fill("golang/go");
    await page.getByRole("button", { name: /subscribe/i }).click();

    const status = page.locator("#status");
    await expect(status).toHaveClass(/status err/);
    await expect(status).toContainText(/network error/i);
  });
});
