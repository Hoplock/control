// ONE LOOK AND FEEL (0060, DESIGN.md §7, §9, §6). Each check holds a rule the
// consistency audit found, or found broken and fixed, across every screen at
// once: the per-screen checks of §10 cannot see two screens that each pass and
// disagree. Each was seen failing on a mutation (docs/learnings/0060-…).

import { readFileSync } from "node:fs";
import { join, relative } from "node:path";
import ts from "typescript";
import { describe, expect, it } from "vitest";
import { STATUS, type Tone } from "../src/components/Status";
import en from "../src/locales/en.json";
import { SCREENS } from "../src/screens/registry";
import { root, src, walk } from "./source";

const catalogue = en as Record<string, string>;

function parse(file: string): ts.SourceFile {
  return ts.createSourceFile(file, readFileSync(file, "utf8"), ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX);
}

function tagName(node: ts.Node): string | undefined {
  if (ts.isJsxOpeningElement(node) || ts.isJsxSelfClosingElement(node)) return node.tagName.getText();
  return undefined;
}

/** Every JSX element in a file, by tag, counted. */
function jsxTags(file: string): Map<string, number> {
  const out = new Map<string, number>();
  const visit = (node: ts.Node) => {
    const tag = tagName(node);
    if (tag) out.set(tag, (out.get(tag) ?? 0) + 1);
    ts.forEachChild(node, visit);
  };
  visit(parse(file));
  return out;
}

/** Where the screens, the shell and sign-in live: everything that is not the component set. */
const outsideTheSet = walk(src, /\.tsx$/).filter((f) => !relative(src, f).startsWith("components/"));
const rel = (f: string) => relative(src, f);

describe("one component per job (item 1)", () => {
  // Each intrinsic element here is a job the component set does: a control,
  // an overlay, an instant. A screen renders the set's, never its own.
  const OWNED: Record<string, string> = {
    button: "Button, IconButton, TextButton",
    input: "Field's Input, Checkbox, Radio, Switch",
    select: "Field's Select",
    textarea: "Field's Textarea",
    dialog: "Dialog, Drawer, ConfirmDialog",
    time: "Timestamp",
  };
  for (const file of outsideTheSet) {
    it(`${rel(file)} renders no control, overlay or instant of its own`, () => {
      const tags = jsxTags(file);
      const own = Object.keys(OWNED).filter((tag) => tags.has(tag));
      expect(own.map((tag) => `<${tag}> — use ${OWNED[tag]}`)).toEqual([]);
    });
  }

  // A list is the data table. These are the tables that are not yet, each a
  // count that may only fall: 0070 moves them onto DataTable. AuditCounts is
  // the second chart (DESIGN.md §11), a table by design.
  const TABLES: Record<string, number> = {
    "screens/AuditCounts.tsx": 1,
    "screens/Deployment.tsx": 1,
    "screens/FleetAlgorithms.tsx": 2,
    "screens/AlgorithmFindings.tsx": 2,
    "screens/PolicyParts.tsx": 1,
    "screens/Runbook.tsx": 1,
  };
  it("renders a list through the data table, but for the tables 0070 moves", () => {
    const found: Record<string, number> = {};
    for (const file of outsideTheSet) {
      const n = jsxTags(file).get("table") ?? 0;
      if (n) found[rel(file)] = n;
    }
    for (const [file, n] of Object.entries(found)) expect(n, `${file}: a <table> of its own; use DataTable`).toBeLessThanOrEqual(TABLES[file] ?? 0);
  });

  // A status hue is the status component's (§3.4). Outside the set, three
  // places draw one on purpose, and only these: Fleet's health bar (§11), a
  // finding card's edge beside its own status pill, and the chrome's
  // supervised and break-glass badges and an unreadable identity (§5).
  const HUES: Record<string, number> = {
    "screens/Fleet.module.css": 5,
    "screens/Policy.module.css": 3,
    "shell/TopBar.module.css": 3,
  };
  it("draws a status hue only in the status component, the set, and three named places", () => {
    const found: Record<string, number> = {};
    for (const file of walk(src, /\.css$/)) {
      const r = rel(file);
      if (r.startsWith("components/") || r.startsWith("styles/")) continue;
      const n = (readFileSync(file, "utf8").match(/var\(--s-/g) ?? []).length;
      if (n) found[r] = n;
    }
    expect(found).toEqual(HUES);
  });

  it("gives a badge a hue only for break-glass (§5: supervised is the chrome's)", () => {
    const toned: string[] = [];
    for (const file of outsideTheSet) {
      for (const m of readFileSync(file, "utf8").matchAll(/<Badge\s+tone="(\w+)"\s*>([^<]*)<\/Badge>/g)) {
        if (!(m[1] === "warn" && m[2]!.includes("breakGlass"))) toned.push(`${rel(file)}: tone="${m[1]}" on ${m[2]}`);
      }
    }
    expect(toned).toEqual([]);
  });
});

describe("page anatomy (item 3) and density (item 5)", () => {
  const registry = parse(join(src, "screens/registry.tsx"));
  const imports = new Map<string, string>();
  registry.forEachChild((node) => {
    if (!ts.isImportDeclaration(node) || !node.importClause?.namedBindings) return;
    const from = (node.moduleSpecifier as ts.StringLiteral).text;
    if (!from.startsWith("./")) return;
    const bindings = node.importClause.namedBindings;
    if (ts.isNamedImports(bindings)) for (const el of bindings.elements) imports.set(el.name.text, join(src, "screens", `${from}.tsx`));
  });

  function rendersPageHeader(name: string, file: string): boolean {
    let found = false;
    const sf = parse(file);
    const visit = (node: ts.Node) => {
      if (ts.isFunctionDeclaration(node) && node.name?.text === name) {
        const inner = (n: ts.Node) => {
          if (tagName(n) === "PageHeader") found = true;
          ts.forEachChild(n, inner);
        };
        inner(node);
      }
      ts.forEachChild(node, visit);
    };
    visit(sf);
    return found;
  }

  for (const [name, file] of imports) {
    it(`${name} heads its page with the page header`, () => {
      expect(rendersPageHeader(name, file)).toBe(true);
    });
  }

  it("defaults the scanned screens to compact and the read ones to comfortable (§3.7)", () => {
    const scanned = (path: string) => path === "fleet" || path.startsWith("fleet/") || path.startsWith("audit/");
    const wrong = SCREENS.filter((s) => s.density !== (scanned(s.path) ? "compact" : "comfortable")).map((s) => `${s.id}: ${s.density}`);
    expect(wrong).toEqual([]);
  });
});

describe("the status vocabulary is DESIGN.md §6's (items 2, 14)", () => {
  const design = readFileSync(join(root, "DESIGN.md"), "utf8");
  const section = design.slice(design.indexOf("## 6. Status vocabulary"), design.indexOf("## 7. Components"));
  const TONE: Record<string, Tone> = {
    "`--s-allow`": "allow",
    "`--s-deny`": "deny",
    "`--s-warn`": "warn",
    "`--s-outage`": "outage",
    "`--s-info`": "info",
    "`--n-400`": "unknown",
  };
  const rows = section
    .split("\n")
    .filter((l) => l.startsWith("| ") && !l.startsWith("| State") && !l.startsWith("| ---"))
    .map((l) => l.split("|").map((c) => c.trim()));

  it("has the same states, by tone and word, as the STATUS map", () => {
    const designed = rows.flatMap((cells) => [...cells[4]!.matchAll(/`([^`]+)`/g)].map((m) => `${TONE[cells[2]!]}: ${m[1]}`)).sort();
    const shipped = Object.values(STATUS)
      .map((s) => `${s.tone}: ${catalogue[s.label]}`)
      .sort();
    expect(shipped).toEqual(designed);
  });

  it("is the one map: no other copies a state's tone and word", () => {
    const copies: string[] = [];
    for (const file of walk(src, /\.tsx?$/)) {
      if (rel(file) === "components/Status.tsx") continue;
      if (/tone: "\w+", label: "status\./.test(readFileSync(file, "utf8"))) copies.push(rel(file));
    }
    expect(copies, "name the STATUS entry instead").toEqual([]);
  });
});

describe("one word per concept (item 8, DESIGN.md §9's glossary)", () => {
  // Each settled term, and the words that must not stand in for it.
  const REFUSED: [RegExp, string][] = [
    [/(?<![\w-])users?(?![\w-])/i, "a person (the human), or an identity (the record): never 'user'"],
    [/^Subject$/, "'Identity' is the label; 'subject' is the policy language's field, set as code"],
    [/\bthis server (took|recorded|no longer holds)\b/i, "what Control decided and holds is this deployment's; 'the server' is only the API answering"],
  ];
  for (const [re, why] of REFUSED) {
    it(`no message uses ${re}: ${why}`, () => {
      expect(Object.entries(catalogue).filter(([, v]) => re.test(v)).map(([k]) => k)).toEqual([]);
    });
  }
});
