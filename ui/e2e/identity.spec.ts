// DESIGN.md §5 and 0050's criterion: the deployment's identity — name, id,
// health, stripe — is legible in a screenshot of any screen, and two
// deployments are told apart from that screenshot alone. The seeded server is
// deployment A ("Staging EU", teal). Deployment B is the same server with its
// identity and stripe substituted in the browser (captioned as such in the
// PR): "Production US", magenta. Both chromes are in the baseline, and this
// test asserts the two images differ where the identity is drawn.

import { expect, test, type BrowserContext, type Page } from "@playwright/test";
import { NORTH, TENANT } from "./harness";
import { openScreen } from "./open";
import { SCREENS } from "./screens";

const fleet = SCREENS.find((s) => s.id === "fleet");
if (!fleet) throw new Error("fleet is not registered");

const CHROME = { x: 0, y: 0, width: 1280, height: 56 };

async function deploymentB(context: BrowserContext): Promise<void> {
  await context.route(`${NORTH}/console/**`, async (route) => {
    if (route.request().resourceType() !== "document") return route.fallback();
    const res = await route.fetch();
    const html = (await res.text()).replace(/data-stripe="[a-z]+"/, 'data-stripe="magenta"');
    return route.fulfill({ response: res, body: html });
  });
  await context.route(`${NORTH}/api/v1/tenants/${TENANT}/instance`, async (route) => {
    const res = await route.fetch();
    const body = (await res.json()) as Record<string, unknown>;
    return route.fulfill({
      response: res,
      json: { ...body, display_name: "Production US", instance_id: "inst-9f31c07ad2e45b6809c1f2e3d4a5b6c7" },
    });
  });
}

/** The share of pixels that differ between two PNGs, decoded by the browser. */
async function difference(page: Page, a: Buffer, b: Buffer): Promise<number> {
  return page.evaluate(
    async ([x, y]) => {
      const load = async (b64: string) => {
        const img = new Image();
        img.src = `data:image/png;base64,${b64}`;
        await img.decode();
        const c = document.createElement("canvas");
        c.width = img.width;
        c.height = img.height;
        const ctx = c.getContext("2d") as CanvasRenderingContext2D;
        ctx.drawImage(img, 0, 0);
        return ctx.getImageData(0, 0, c.width, c.height).data;
      };
      const [p, q] = await Promise.all([load(x), load(y)]);
      let differ = 0;
      for (let i = 0; i < p.length; i += 4) {
        if (Math.abs((p[i] as number) - (q[i] as number)) + Math.abs((p[i + 1] as number) - (q[i + 1] as number)) + Math.abs((p[i + 2] as number) - (q[i + 2] as number)) > 48) differ++;
      }
      return differ / (p.length / 4);
    },
    [a.toString("base64"), b.toString("base64")] as const,
  );
}

test("two deployments are told apart from a screenshot of the chrome alone", async ({ browser }) => {
  const a = await openScreen(browser, fleet);
  await expect(a.locator("header").first()).toContainText("Staging EU");
  console.log(`[chrome] ${(await a.locator("header").first().innerText()).replace(/\s+/g, " ")}`);
  await expect(a).toHaveScreenshot("identity-deployment-a.png", { clip: CHROME });
  const shotA = await a.screenshot({ clip: CHROME });

  const b = await openScreen(browser, fleet, { before: deploymentB });
  await expect(b.locator("header").first()).toContainText("Production US");
  await expect(b).toHaveScreenshot("identity-deployment-b.png", { clip: CHROME });
  const shotB = await b.screenshot({ clip: CHROME });

  // The stripe, the chip's tint, the name and the id all change: far more
  // than antialiasing could account for.
  expect(await difference(b, shotA, shotB)).toBeGreaterThan(0.05);
  await a.context().close();
  await b.context().close();
});
