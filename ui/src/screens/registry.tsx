// THE CONSOLE'S SCREENS. A later phase adds a screen by adding one entry
// here — its path inside /console/, its navigation label (a catalogue key),
// its density default (DESIGN.md §3.7) and the permission its main read
// needs — and its catalogue keys to en.json, and its routes to api/routes.ts.
// The navigation lists exactly these, in this order, so it never names a
// screen that does not exist.

import type { ReactNode } from "react";
import type { MessageKey } from "../i18n/catalogue";
import { DeploymentScreen } from "./Deployment";
import { FleetScreen } from "./Fleet";

export interface Screen {
  /** Its stable id, used in tests and in 0050's screen registry. */
  id: string;
  /** Its path inside the console, with :params: a deep link, so stable. */
  path: string;
  /** Its entry in the navigation, or none for a detail page. */
  nav?: MessageKey;
  /** Compact on scanned screens (Fleet, Audit); comfortable on read ones. */
  density: "compact" | "comfortable";
  /** The permission the screen's main read needs, if any (a courtesy: RBAC is the API's). */
  permission?: string;
  render(params: Record<string, string>): ReactNode;
}

export const SCREENS: readonly Screen[] = [
  {
    id: "deployment",
    path: "deployment",
    nav: "nav.deployment",
    density: "comfortable",
    permission: "instance:read",
    render: () => <DeploymentScreen />,
  },
  {
    id: "fleet",
    path: "fleet",
    nav: "nav.fleet",
    density: "compact",
    permission: "fleet:read",
    render: () => <FleetScreen />,
  },
];

/** Where /console/ itself goes. */
export const HOME = "deployment";
